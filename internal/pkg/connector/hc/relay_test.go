package hc

import (
	"github.com/google/uuid"
	"github.com/raf924/bot/pkg/relay"
	"github.com/raf924/connector-api/pkg/gen"
	"os"
	"testing"
)

func setupHCRelay(tb testing.TB) *hCRelay {
	hcRelay := newHCRelay(hcRelayConfig{
		Trigger: "",
		Secure:  true,
		Url:     "wss://hack.chat/chat-ws",
		Channel: "botDev",
		Retries: struct {
			Force      bool   `yaml:"force"`
			MaxRetries int    `yaml:"max"`
			Delay      string `yaml:"delay"`
		}{},
		Password: os.ExpandEnv("${HC_PASSWORD}"),
	})
	err := hcRelay.Connect("bot")
	if err != nil {
		tb.Errorf("unexpected error: %v", err)
	}
	return hcRelay
}

func roundTrip(hcRelay *hCRelay, text string, tb testing.TB) *gen.MessagePacket {
	err := hcRelay.Send(relay.ChatMessage{
		Message:   text,
		Recipient: "",
		Private:   false,
	})
	if err != nil {
		tb.Errorf("unexpected error: %v", err)
	}
	m := &gen.MessagePacket{}
	err = hcRelay.RecvMsg(m)
	if err != nil {
		tb.Errorf("unexpected error: %v", err)
	}
	return m
}

func TestHCRelayRoundTrip(t *testing.T) {
	hcRelay := setupHCRelay(t)
	text := uuid.NewString()
	m := roundTrip(hcRelay, text, t)
	if m.Message != text {
		t.Errorf("expected %v got %v", text, m.Message)
	}
}

func BenchmarkRoundTrip(b *testing.B) {
	hcRelay := setupHCRelay(b)
	for i := 0; i < b.N; i++ {
		_ = roundTrip(hcRelay, "test", b)
	}
}
