package hc

import (
	"github.com/google/uuid"
	"github.com/raf924/bot/pkg/queue"
	"github.com/raf924/bot/pkg/relay/connection"
	"github.com/raf924/connector-api/pkg/gen"
	"os"
	"testing"
)

func setupHCRelay(tb testing.TB, ex *queue.Exchange) *hCRelay {
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
	}, ex)
	err := hcRelay.Connect("bot")
	if err != nil {
		tb.Errorf("unexpected error: %v", err)
	}
	return hcRelay
}

func roundTrip(tb testing.TB, ex *queue.Exchange, text string) *gen.MessagePacket {
	err := ex.Produce(connection.ChatMessage{
		Message:   text,
		Recipient: "",
		Private:   false,
	})
	if err != nil {
		tb.Errorf("unexpected error: %v", err)
	}
	m, err := ex.Consume()
	if err != nil {
		tb.Errorf("unexpected error: %v", err)
	}
	return m.(*gen.MessagePacket)
}

func TestHCRelayRoundTrip(t *testing.T) {
	var cn2CrQ = queue.NewQueue()
	var cr2CnQ = queue.NewQueue()
	var cnEx, _ = queue.NewExchange(cr2CnQ, cn2CrQ)
	var crEx, _ = queue.NewExchange(cn2CrQ, cr2CnQ)
	_ = setupHCRelay(t, crEx)
	text := uuid.NewString()
	m := roundTrip(t, cnEx, text)
	if m.Message != text {
		t.Errorf("expected %v got %v", text, m.Message)
	}
}

func BenchmarkRoundTrip(b *testing.B) {
	var cn2CrQ = queue.NewQueue()
	var cr2CnQ = queue.NewQueue()
	var cnEx, _ = queue.NewExchange(cr2CnQ, cn2CrQ)
	var crEx, _ = queue.NewExchange(cn2CrQ, cr2CnQ)
	_ = setupHCRelay(b, crEx)
	for i := 0; i < b.N; i++ {
		_ = roundTrip(b, cnEx, "test")
	}
}
