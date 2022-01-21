package hc

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/raf924/connector-sdk/domain"
	"github.com/raf924/connector-sdk/queue"
	"github.com/raf924/connector-sdk/rpc"
	"gopkg.in/yaml.v2"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var _ rpc.ConnectionRelay = (*hCRelay)(nil)

var mentionRegex = regexp.MustCompile("@+(\\w+)")

func NewHCRelay(config interface{}) rpc.ConnectionRelay {
	data, err := yaml.Marshal(config)
	if err != nil {
		panic(err)
	}
	var conf HCRelayConfig
	if err := yaml.Unmarshal(data, &conf); err != nil {
		panic(err)
	}
	return newHCRelay(conf)
}

func newHCRelay(config HCRelayConfig) *hCRelay {
	return &hCRelay{
		config: config,
		users:  domain.NewUserList(),
		onUserJoin: func(user *domain.User, timestamp time.Time) {

		},
		onUserLeft: func(user *domain.User, timestamp time.Time) {

		},
	}
}

type hCRelay struct {
	config         HCRelayConfig
	conn           *websocket.Conn
	nick           string
	wsUrl          *url.URL
	retries        int
	delay          time.Duration
	currentUser    *domain.User
	users          domain.UserList
	onUserJoin     func(user *domain.User, timestamp time.Time)
	onUserLeft     func(user *domain.User, timestamp time.Time)
	serverProducer queue.Producer
	serverConsumer queue.Consumer
}

func (h *hCRelay) Recv() (*domain.ChatMessage, error) {
	v, err := h.serverConsumer.Consume()
	return v.(*domain.ChatMessage), err
}

func (h *hCRelay) Send(message *domain.ClientMessage) error {
	return h.sendToServer(message)
}

func (h *hCRelay) addUser(user *domain.User) {
	h.users.Add(user)
}

func (h *hCRelay) removeUser(user *domain.User) {
	h.users.Remove(user)
}

func convertTo(jsonData map[string]interface{}, obj interface{}) error {
	data, err := json.Marshal(jsonData)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, obj)
}

func (h *hCRelay) OnUserJoin(f func(user *domain.User, timestamp time.Time)) {
	h.onUserJoin = func(user *domain.User, timestamp time.Time) {
		//log.Printf("%s joined\n", user.Nick)
		f(user, timestamp)
	}
}

func (h *hCRelay) OnUserLeft(f func(user *domain.User, timestamp time.Time)) {
	h.onUserLeft = func(user *domain.User, timestamp time.Time) {
		//log.Printf("%s left\n", user.Nick)
		f(user, timestamp)
	}
}

func (h *hCRelay) CommandTrigger() string {
	return h.config.Trigger
}

const defaultDelay = 5 * time.Second

func (h *hCRelay) connect(nick string) error {
	h.nick = nick
	wsUrl, err := url.Parse(h.config.Url)
	if err != nil {
		return err
	}
	h.wsUrl = wsUrl
	h.retries = 0
	h.delay, err = time.ParseDuration(h.config.Retries.Delay)
	if err != nil {
		h.delay = defaultDelay
	}
	err = h.waitForConnection()
	if err != nil {
		return err
	}
	log.Println("Connected")
	h.retries = 0
	response, err := h.waitForJoinConfirmation()
	if err != nil {
		return err
	}
	for _, hcUser := range response.Users {
		log.Println("hcUser:", hcUser.Nick)
		onlineUser := hcUser.toOnlineUser(time.Now())
		h.addUser(onlineUser)
		if hcUser.IsCurrentUser {
			h.currentUser = onlineUser
		}
	}
	err = h.conn.SetReadDeadline(time.Time{})
	if err != nil {
		return err
	}
	return nil
}

func (h *hCRelay) readServerMessage(producer queue.Producer) error {
	var response mapPacket
	if err := h.conn.ReadJSON(&response); err != nil {
		return fmt.Errorf("failed to read message: %v", err)
	}
	return producer.Produce(response)
}

func (h *hCRelay) readServerMessages(producer queue.Producer) error {
	for {
		err := h.readServerMessage(producer)
		if err != nil {
			return err
		}
	}
}

func (h *hCRelay) relayServerMessage(consumer queue.Consumer) error {
	var serverResponse interface{}
	serverResponse, err := consumer.Consume()
	if err != nil {
		return err
	}
	response := serverResponse.(mapPacket)
	switch response.GetCommand() {
	case userJoin:
		ujp := userJoinPacket{}
		_ = convertTo(response, &ujp)
		newUser := ujp.toOnlineUser(time.Now())
		h.addUser(newUser)
		if h.onUserJoin != nil {
			h.onUserJoin(newUser, time.UnixMilli(ujp.Timestamp))
		}
	case userLeft:
		ulp := userLeftPacket{}
		_ = convertTo(response, &ulp)
		quitter := ulp.toUser()
		h.removeUser(quitter)
		if h.onUserLeft != nil {
			h.onUserLeft(quitter, time.UnixMilli(ulp.Timestamp))
		}
	case info:
		ip := infoPacket{}
		_ = convertTo(response, &ip)
		switch ip.Type {
		case whisper:
			wp := whisperServerPacket{}
			_ = convertTo(response, &wp)
			text := strings.TrimSpace(strings.Join(strings.Split(wp.Text, ":")[1:], ":"))
			user := h.users.Find(wp.From)
			if user == nil {
				break
			}
			mp := domain.NewChatMessage(text, user, nil, false, true, time.UnixMilli(wp.Timestamp), !user.Is(h.currentUser))
			err := h.sendToConnector(mp)
			if err != nil {
				return err
			}
		}
	case chat:
		cp := chatServerPacket{}
		_ = convertTo(response, &cp)
		user := h.users.Find(cp.Nick)
		matches := mentionRegex.FindAllStringSubmatch(cp.Text, -1)
		var recipients []*domain.User
		var mentionsConnectorUser bool
		for _, match := range matches {
			nick := match[1]
			if nick == h.nick {
				mentionsConnectorUser = true
			}
			recipient := h.users.Find(nick)
			if recipient != nil {
				recipients = append(recipients, recipient)
			}
		}
		mp := domain.NewChatMessage(strings.TrimSpace(cp.Text), user, recipients, mentionsConnectorUser, false, time.UnixMilli(cp.Timestamp), !user.Is(h.currentUser))
		err := h.sendToConnector(mp)
		if err != nil {
			return err
		}
	}
	return nil
}

func (h *hCRelay) relayServerMessages(consumer queue.Consumer) error {
	for {
		err := h.relayServerMessage(consumer)
		if err != nil {
			return err
		}
	}
}

func (h *hCRelay) Connect(nick string) (*domain.User, domain.UserList, error) {
	var err error
	serverQueue := queue.NewQueue()
	h.serverProducer, err = serverQueue.NewProducer()
	if err != nil {
		return nil, nil, err
	}
	h.serverConsumer, err = serverQueue.NewConsumer()
	if err != nil {
		return nil, nil, err
	}
	internalQueue := queue.NewQueue()
	internalProducer, err := internalQueue.NewProducer()
	if err != nil {
		return nil, nil, err
	}
	internalConsumer, err := internalQueue.NewConsumer()
	if err != nil {
		return nil, nil, err
	}
	var connected bool
	var connectionChan = make(chan bool, 1)
	go func() {
		for {
			err := h.connect(nick)
			if err != nil {
				panic(err)
			}
			if !connected {
				connected = true
				connectionChan <- connected
			}
			err = h.readServerMessages(internalProducer)
			if err != nil {
				log.Println(err)
				err = h.conn.Close()
				if err != nil {
					panic(err)
				}
			}
		}
	}()
	go func() {
		err := h.relayServerMessages(internalConsumer)
		if err != nil {
			panic(err)
		}
	}()
	<-connectionChan
	go func() {
		timer := time.NewTimer(50 * time.Second)
		for ok := true; ok; _, ok = <-timer.C {
			err := h.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(30*time.Second))
			if err != nil {
				log.Println("ping error", err)
			}
		}
	}()
	return h.currentUser, domain.ImmutableUserList(h.users), nil
}

func (h *hCRelay) sendToConnector(m *domain.ChatMessage) error {
	return h.serverProducer.Produce(m)
}

func (h *hCRelay) canRetry() bool {
	if h.config.Retries.MaxRetries < 0 {
		return true
	}
	return h.config.Retries.MaxRetries >= h.retries
}

func (h *hCRelay) waitForConnection() error {
	var err error
	for h.canRetry() {
		err = h.connectTry()
		if err == nil {
			break
		}
		log.Println(err)
		time.Sleep(h.delay)
	}
	return err
}

func (h *hCRelay) connectTry() error {
	log.Printf("connecting to %s\n", h.wsUrl.String())
	conn, _, err := websocket.DefaultDialer.Dial(h.wsUrl.String(), http.Header{"Origin": {"hack.chat"}})
	if err != nil {
		return err
	}
	if conn == nil {
		return errors.New("wtf")
	}
	h.conn = conn
	return nil
}

func (h *hCRelay) joinTry() error {
	err := h.conn.WriteJSON(joinCommand{
		Command: string(clientJoin),
		Channel: h.config.Channel,
		Nick:    fmt.Sprintf("%s#%s", h.nick, h.config.Password),
	})
	return err
}

func (h *hCRelay) waitForJoinConfirmation() (*joinedPacket, error) {
	var response joinedPacket
	var origNick = h.nick
	for h.canRetry() {
		err := h.joinTry()
		if err != nil {
			log.Println("couldn't join")
			time.Sleep(h.delay)
			continue
		}
		err = h.conn.ReadJSON(&response)
		if err != nil {
			return nil, err
		}
		if response.GetCommand() == string(join) {
			return &response, nil
		}
		log.Println("couldn't connect")
		if h.config.Retries.Force {
			b := make([]byte, 5)
			_, _ = rand.Read(b)
			h.nick = fmt.Sprintf("%s_%s", origNick, base64.StdEncoding.EncodeToString(b)[:5])
		}
		time.Sleep(h.delay)
	}
	return nil, errors.New("exceeded allowed number of retries")
}

func (h *hCRelay) sendToServer(message *domain.ClientMessage) error {
	var packet interface{}
	var text = ""
	if message.Private() {
		if message.Recipient() == nil {
			log.Println("can't send private message to no one")
			return nil
		}
		text = fmt.Sprintf("%swhisper ", h.config.Trigger)
	}
	if message.Recipient() != nil {
		text += fmt.Sprintf("@%s", message.Recipient().Nick())
	}
	packet = struct {
		chatPacket
		Command clientCommand `json:"cmd"`
	}{
		chatPacket: chatPacket{Text: fmt.Sprintf("%s %s", text, message.Message())},
		Command:    clientChat,
	}
	return h.conn.WriteJSON(packet)
}
