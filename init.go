package hc_connection_relay

import (
	"github.com/raf924/bot/pkg/rpc"
	"github.com/raf924/hc-connection-relay/internal/pkg/connector/hc"
)

func init() {
	rpc.RegisterConnectionRelay("hc", hc.NewHCRelay)
}

type HCConfig = hc.HCRelayConfig
