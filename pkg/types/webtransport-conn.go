package types

import (
	wt "github.com/zishang520/webtransport-go"
	"github.com/winking324/socket.io/v3/pkg/webtransport"
)

type WebTransportConn struct {
	EventEmitter

	*webtransport.Conn
}

func (t *WebTransportConn) CloseWithError(code wt.SessionErrorCode, msg string) error {
	defer t.Emit("close")
	return t.Conn.CloseWithError(code, msg)
}
