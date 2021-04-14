package hc_connection_relay

import (
	"github.com/raf924/bot/pkg/queue"
	"github.com/raf924/bot/pkg/relay/connection"
	"github.com/raf924/hc-connection-relay/internal/pkg/connector/hc"
)

func init() {
	connection.RegisterConnectionRelay("hc", func(config interface{}, connectorExchange *queue.Exchange) connection.Relay {
		return hc.NewHCRelay(config, connectorExchange)
	})
}

type HCConfig = hc.HCRelayConfig
