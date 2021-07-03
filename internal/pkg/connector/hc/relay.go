package hc

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/raf924/bot/pkg/queue"
	"github.com/raf924/bot/pkg/relay/connection"
	"github.com/raf924/bot/pkg/users"
	messages "github.com/raf924/connector-api/pkg/gen"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/yaml.v2"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func NewHCRelay(config interface{}) *hCRelay {
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
		users:  users.NewUserList(),
		onUserJoin: func(user *messages.User, timestamp int64) {

		},
		onUserLeft: func(user *messages.User, timestamp int64) {

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
	users          *users.UserList
	onUserJoin     func(user *messages.User, timestamp int64)
	onUserLeft     func(user *messages.User, timestamp int64)
	serverProducer *queue.Producer
	serverConsumer *queue.Consumer
}

func (h *hCRelay) Recv() (*messages.MessagePacket, error) {
	v, err := h.serverConsumer.Consume()
	return v.(*messages.MessagePacket), err
}

func (h *hCRelay) Send(message connection.Message) error {
	return h.sendToServer(message)
}

func (h *hCRelay) GetUsers() *users.UserList {
	return h.users.Copy()
}

func (h *hCRelay) addUser(user *messages.User) {
	h.users.Add(user)
}

func (h *hCRelay) removeUser(user *messages.User) {
	h.users.Remove(user)
}

func convertTo(jsonData map[string]interface{}, obj interface{}) error {
	data, err := json.Marshal(jsonData)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, obj)
}

func (h *hCRelay) OnUserJoin(f func(user *messages.User, timestamp int64)) {
	h.onUserJoin = func(user *messages.User, timestamp int64) {
		//log.Printf("%s joined\n", user.Nick)
		f(user, timestamp)
	}
}

func (h *hCRelay) OnUserLeft(f func(user *messages.User, timestamp int64)) {
	h.onUserLeft = func(user *messages.User, timestamp int64) {
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
	log.Println("Connected")
	if err != nil {
		return err
	}
	h.retries = 0
	response, err := h.waitForJoinConfirmation()
	if err != nil {
		return err
	}
	for _, user := range response.Users {
		log.Println("hcUser:", user.Nick)
		h.addUser(&messages.User{
			Nick:  user.Nick,
			Id:    user.Trip,
			Mod:   user.UserType == "mod",
			Admin: user.UserType == "admin",
		})
	}
	return nil
}

func (h *hCRelay) readServerMessage(producer *queue.Producer) error {
	var response mapPacket
	if err := h.conn.ReadJSON(&response); err != nil {
		return err
	}
	return producer.Produce(response)
}

func (h *hCRelay) readServerMessages(producer *queue.Producer) error {
	for {
		err := h.readServerMessage(producer)
		if err != nil {
			return err
		}
	}
}

func (h *hCRelay) relayServerMessage(consumer *queue.Consumer) error {
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
		newUser := &messages.User{
			Nick:  ujp.Nick,
			Id:    ujp.Trip,
			Mod:   ujp.UserType == "mod",
			Admin: ujp.UserType == "admin",
		}
		h.addUser(newUser)
		if h.onUserJoin != nil {
			h.onUserJoin(newUser, ujp.Timestamp)
		}
	case userLeft:
		ulp := userLeftPacket{}
		_ = convertTo(response, &ulp)
		quitter := &messages.User{
			Nick: ulp.Nick,
		}
		h.removeUser(quitter)
		if h.onUserLeft != nil {
			h.onUserLeft(quitter, ulp.Timestamp)
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
			mp := messages.MessagePacket{
				Timestamp: timestamppb.New(time.Unix(0, wp.Timestamp*int64(time.Millisecond))),
				Message:   text,
				Private:   true,
				User:      user,
			}
			err := h.sendToConnector(&mp)
			if err != nil {
				return err
			}
		}
	case chat:
		cp := chatServerPacket{}
		_ = convertTo(response, &cp)
		mp := messages.MessagePacket{
			Timestamp: timestamppb.New(time.Unix(0, cp.Timestamp*int64(time.Millisecond))),
			Message:   strings.TrimSpace(cp.Text),
			User: &messages.User{
				Nick:  cp.Nick,
				Id:    cp.Trip,
				Mod:   cp.UserType == "mod",
				Admin: cp.UserType == "admin",
			},
			Private: false,
		}
		err := h.sendToConnector(&mp)
		if err != nil {
			return err
		}
	}
	return nil
}

func (h *hCRelay) relayServerMessages(consumer *queue.Consumer) error {
	for {
		err := h.relayServerMessage(consumer)
		if err != nil {
			return err
		}
	}
}

func (h *hCRelay) Connect(nick string) error {
	err := h.connect(nick)
	if err != nil {
		return err
	}
	serverQueue := queue.NewQueue()
	h.serverProducer, err = serverQueue.NewProducer()
	if err != nil {
		return err
	}
	h.serverConsumer, err = serverQueue.NewConsumer()
	if err != nil {
		return err
	}
	internalQueue := queue.NewQueue()
	internalProducer, err := internalQueue.NewProducer()
	if err != nil {
		return err
	}
	internalConsumer, err := internalQueue.NewConsumer()
	if err != nil {
		return err
	}
	go func() {
		err := h.readServerMessages(internalProducer)
		if err != nil {
			panic(err)
		}
	}()
	go func() {
		err := h.relayServerMessages(internalConsumer)
		if err != nil {
			panic(err)
		}
	}()
	return nil
}

func (h *hCRelay) sendToConnector(m *messages.MessagePacket) error {
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
		Command: "join",
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
			var b []byte
			_, _ = rand.Read(b)
			h.nick = fmt.Sprintf("%s_%s", origNick, base64.StdEncoding.EncodeToString(b)[:5])
		}
		time.Sleep(h.delay)
	}
	return nil, errors.New("exceeded allowed number of retries")
}

func (h *hCRelay) sendToServer(message connection.Message) error {
	var packet interface{}
	switch message.(type) {
	case connection.ChatMessage:
		message := message.(connection.ChatMessage)
		var text = ""
		if message.Private {
			if message.Recipient == "" {
				log.Println("can't send private message to no one")
				return nil
			}
			text = fmt.Sprintf("%swhisper ", h.config.Trigger)
		}
		if message.Recipient != "" {
			text += fmt.Sprintf("@%s", message.Recipient)
		}
		packet = struct {
			chatPacket
			Command clientCommand `json:"cmd"`
		}{
			chatPacket: chatPacket{Text: fmt.Sprintf("%s %s", text, message.Message)},
			Command:    clientChat,
		}
	case connection.NoticeMessage:
		message := message.(connection.NoticeMessage)
		packet = struct {
			emotePacket
			Command clientCommand `json:"cmd"`
		}{
			Command:     clientChat,
			emotePacket: emotePacket{chatPacket{Text: fmt.Sprintf("%sme %s", h.config.Trigger, message.Message)}},
		}
		break
	case connection.InviteMessage:
		message := message.(connection.InviteMessage)
		if message.Recipient == "" {
			log.Println("can't invite no one")
			return nil
		}
		packet = struct {
			invitePacket
			Command clientCommand `json:"cmd"`
		}{
			invitePacket: invitePacket{Nick: message.Recipient},
			Command:      clientInvite,
		}
	default:
		return nil
	}
	return h.conn.WriteJSON(packet)
}
