package hc

import (
	"github.com/raf924/bot/pkg/relay"
	"github.com/raf924/hc-connection-relay/internal/pkg/connector/hc"
)

func init() {
	relay.RegisterConnectionRelay("hc", hc.NewHCRelay)
}
