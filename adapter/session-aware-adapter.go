package adapter

import (
	"github.com/zishang520/socket.io/v2/socket"
)

type (
	SessionAwareAdapterBuilder struct {
	}
)

func (*SessionAwareAdapterBuilder) New(nsp socket.Namespace) Adapter {
	return NewSessionAwareAdapter(nsp)
}

func MakeSessionAwareAdapter() SessionAwareAdapter {
	return socket.MakeSessionAwareAdapter()
}

func NewSessionAwareAdapter(nsp socket.Namespace) SessionAwareAdapter {
	return socket.NewSessionAwareAdapter(nsp)
}
