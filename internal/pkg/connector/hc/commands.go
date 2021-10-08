package hc

import (
	"github.com/raf924/bot/pkg/domain"
	"time"
)

type serverCommand string

const (
	join     serverCommand = "onlineSet"
	userJoin serverCommand = "onlineAdd"
	userLeft serverCommand = "onlineRemove"
	warn     serverCommand = "warn"
	info     serverCommand = "info"
	emote    serverCommand = "emote"
	chat     serverCommand = "chat"
	unknown  serverCommand = "unknown"
)

type clientCommand string

const (
	clientJoin   clientCommand = "join"
	clientInvite clientCommand = "invite"
	clientChat   clientCommand = "chat"
)

type infoType string

const (
	whisper infoType = "whisper"
)

type joinCommand struct {
	Command string `json:"cmd"`
	Channel string `json:"channel"`
	Nick    string `json:"nick"`
}

type hcPacket interface {
	GetCommand() string
}

type sP interface {
	GetCommand() serverCommand
	GetTimestamp() int64
	GetChannel() string
}

type mapPacket map[string]interface{}

func (m mapPacket) GetCommand() serverCommand {
	switch m["cmd"].(type) {
	case serverCommand:
		return m["cmd"].(serverCommand)
	case string:
		switch m["cmd"].(string) {
		case string(warn):
			return warn
		case string(join):
			return join
		case string(chat):
			return chat
		case string(info):
			return info
		case string(userJoin):
			return userJoin
		case string(emote):
			return emote
		case string(userLeft):
			return userLeft
		default:
			return unknown
		}
	default:
		return unknown
	}
}

func (m mapPacket) GetTimestamp() int64 {
	return m["time"].(int64)
}

func (m mapPacket) GetChannel() string {
	return m["channel"].(string)
}

type serverPacket struct {
	Command   serverCommand `json:"cmd"`
	Timestamp int64         `json:"time"`
	Channel   interface{}   `json:"channel"`
}

func (s serverPacket) GetCommand() string {
	return string(s.Command)
}

func (s serverPacket) GetTimestamp() int64 {
	return s.Timestamp
}

func (s serverPacket) GetChannel() string {
	return s.Channel.(string)
}

type warnPacket struct {
	serverPacket
	Text string `json:"text"`
}

type serverUser struct {
	Nick          string `json:"nick"`
	UserId        int64  `json:"userid"`
	Channel       string `json:"channel"`
	IsCurrentUser bool   `json:"isme"`
}

func (u *serverUser) toUser() *domain.User {
	return domain.NewUser(u.Nick, "", domain.RegularUser)
}

type user struct {
	serverUser
	Trip     string `json:"trip"`
	UserType string `json:"uType"`
	Hash     string `json:"hash"`
}

func (u *user) toUser() *domain.User {
	var role domain.UserRole
	switch u.UserType {
	case "admin":
		role = domain.Admin
	case "mod":
		role = domain.Moderator
	default:
		role = domain.RegularUser
	}
	return domain.NewUser(u.Nick, u.Trip, role)
}

func (u *user) toOnlineUser(joinTime time.Time) *domain.User {
	var role domain.UserRole
	switch u.UserType {
	case "admin":
		role = domain.Admin
	case "mod":
		role = domain.Moderator
	default:
		role = domain.RegularUser
	}
	return domain.NewOnlineUser(u.Nick, u.Trip, role, &joinTime)
}

type joinedPacket struct {
	serverPacket
	Nicks []string `json:"nicks"`
	Users []user   `json:"users"`
}

type userJoinPacket struct {
	serverPacket
	user
}

type userLeftPacket struct {
	serverPacket
	serverUser
}

type chatServerPacket struct {
	serverPacket
	user
	Text string `json:"text"`
}

type infoPacket struct {
	serverPacket
	Text string   `json:"text"`
	Type infoType `json:"type"`
}

type whisperServerPacket struct {
	serverPacket
	From string `json:"from"`
	Trip string `json:"trip"`
	Text string `json:"text"`
}

type clientPacket struct {
	Command string `json:"command"`
	cPacket
}

type cPacket interface {
	hcPacket
}

type chatPacket struct {
	Text string `json:"text"`
}

func (c chatPacket) GetCommand() string {
	return "chat"
}

type whisperPacket struct {
	chatPacket
}

type emotePacket struct {
	chatPacket
}

type invitePacket struct {
	Nick string
}

func (i invitePacket) GetCommand() string {
	return "invite"
}
