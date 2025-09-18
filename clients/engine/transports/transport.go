package transports

import (
	"github.com/winking324/socket.io/clients/engine/v3"
)

type (
	TransportCtor = engine.TransportCtor

	WebSocketBuilder    = engine.WebSocketBuilder
	WebTransportBuilder = engine.WebTransportBuilder
	PollingBuilder      = engine.PollingBuilder
)

var (
	Polling      TransportCtor = &PollingBuilder{}
	WebSocket    TransportCtor = &WebSocketBuilder{}
	WebTransport TransportCtor = &WebTransportBuilder{}
)
