package hc

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/raf924/bot/pkg/relay"
	messages "github.com/raf924/connector-api/pkg/gen"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/yaml.v2"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func NewHCRelay(config interface{}) relay.ConnectionRelay {
	data, err := yaml.Marshal(config)
	if err != nil {
		panic(err)
	}
	var conf hcRelayConfig
	if err := yaml.Unmarshal(data, &conf); err != nil {
		panic(err)
	}
	return &hCRelay{config: conf, serverChannel: make(chan messages.MessagePacket)}
}

type hCRelay struct {
	config        hcRelayConfig
	conn          *websocket.Conn
	nick          string
	wsUrl         *url.URL
	retries       int
	delay         time.Duration
	serverChannel chan messages.MessagePacket
	users         []*messages.User
	onUserJoin    func(user *messages.User, timestamp int64)
	onUserLeft    func(user *messages.User, timestamp int64)
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
		log.Printf("%s joined\n", user.Nick)
		f(user, timestamp)
	}
}

func (h *hCRelay) OnUserLeft(f func(user *messages.User, timestamp int64)) {
	h.onUserLeft = func(user *messages.User, timestamp int64) {
		log.Printf("%s left\n", user.Nick)
		f(user, timestamp)
	}
}

func (h *hCRelay) GetUsers() []*messages.User {
	//TODO: map ?
	return h.users
}

func (h *hCRelay) CommandTrigger() string {
	return "/"
}

const defaultDelay = 5 * time.Second

func (h *hCRelay) Connect(nick string) error {
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
		newUser := &messages.User{
			Nick:  user.Nick,
			Id:    user.Trip,
			Mod:   user.UserType == "mod",
			Admin: user.UserType == "admin",
		}
		h.users = append(h.users, newUser)
	}
	go func() {
		for true {
			var response mapPacket
			if err := h.conn.ReadJSON(&response); err != nil {
				panic(err)
			}
			go func() {
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
					h.users = append(h.users, newUser)
					if h.onUserJoin != nil {
						h.onUserJoin(newUser, ujp.Timestamp)
					}
				case userLeft:
					ulp := userLeftPacket{}
					_ = convertTo(response, &ulp)
					newUser := &messages.User{
						Nick: ulp.Nick,
					}
					for i, u := range h.users {
						if u.Nick == ulp.Nick {
							h.users = append(h.users[:i], h.users[i+1:]...)
							break
						}
					}
					if h.onUserLeft != nil {
						h.onUserLeft(newUser, ulp.Timestamp)
					}
				case info:
					ip := infoPacket{}
					_ = convertTo(response, &ip)
					switch ip.Type {
					case whisper:
						wp := whisperServerPacket{}
						_ = convertTo(response, &wp)
						text := strings.TrimSpace(strings.Join(strings.Split(wp.Text, ":")[1:], ":"))
						mp := messages.MessagePacket{
							Timestamp: timestamppb.New(time.Unix(0, wp.Timestamp*int64(time.Millisecond))),
							Message:   text,
							Private:   true,
							User: &messages.User{
								Nick: wp.From,
								Id:   wp.Trip,
							},
						}
						h.serverChannel <- mp
					}
				case chat:
					cp := chatServerPacket{}
					_ = convertTo(response, &cp)
					mp := messages.MessagePacket{
						Timestamp: timestamppb.New(time.Unix(0, cp.Timestamp*int64(time.Millisecond))),
						Message:   cp.Text,
						User: &messages.User{
							Nick:  cp.Nick,
							Id:    cp.Trip,
							Mod:   cp.UserType == "mod",
							Admin: cp.UserType == "admin",
						},
						Private: false,
					}
					h.serverChannel <- mp
				}
			}()
		}
	}()
	return nil
}

func (h hCRelay) canRetry() bool {
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

func (h *hCRelay) Send(message relay.Message) error {
	var packet interface{}
	switch message.(type) {
	case relay.ChatMessage:
		message := message.(relay.ChatMessage)
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
	case relay.NoticeMessage:
		message := message.(relay.NoticeMessage)
		packet = struct {
			emotePacket
			Command clientCommand `json:"cmd"`
		}{
			Command:     clientChat,
			emotePacket: emotePacket{chatPacket{Text: fmt.Sprintf("%sme %s", h.config.Trigger, message.Message)}},
		}
		break
	case relay.InviteMessage:
		message := message.(relay.InviteMessage)
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

func (h *hCRelay) Recv() (*messages.MessagePacket, error) {
	var packet *messages.MessagePacket
	return packet, h.RecvMsg(packet)
}

func (h *hCRelay) RecvMsg(packet *messages.MessagePacket) error {
	var ok bool
	*packet, ok = <-h.serverChannel
	if !ok {
		return io.EOF
	}
	return nil
}
