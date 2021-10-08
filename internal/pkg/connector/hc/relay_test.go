package hc

import (
	"github.com/google/uuid"
	"github.com/raf924/bot/pkg/domain"
	"os"
	"testing"
)

func setupHCRelay(tb testing.TB) *hCRelay {
	hcRelay := newHCRelay(HCRelayConfig{
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
	_, _, err := hcRelay.Connect("bot")
	if err != nil {
		tb.Errorf("unexpected error: %v", err)
	}
	return hcRelay
}

func roundTrip(tb testing.TB, hcR *hCRelay, text string) *domain.ChatMessage {
	err := hcR.Send(domain.NewClientMessage(text, nil, false))
	if err != nil {
		tb.Errorf("unexpected error: %v", err)
	}
	m, err := hcR.Recv()
	if err != nil {
		tb.Errorf("unexpected error: %v", err)
	}
	return m
}

func TestHCRelayRoundTrip(t *testing.T) {
	hcR := setupHCRelay(t)
	text := uuid.NewString()
	m := roundTrip(t, hcR, text)
	if m.Message() != text {
		t.Errorf("expected %v got %v", text, m.Message())
	}
}

func BenchmarkRoundTrip(b *testing.B) {
	hcR := setupHCRelay(b)
	for i := 0; i < b.N; i++ {
		_ = roundTrip(b, hcR, "test")
	}
}
