package adapter

import (
	"testing"

	"github.com/winking324/socket.io/servers/socket/v3"
)

func TestClusterAdapterWithHeartbeatBuilder(t *testing.T) {
	builder := &ClusterAdapterWithHeartbeatBuilder{
		Opts: nil,
	}

	builder.New(socket.NewNamespace(socket.NewServer(nil, nil), "/test"))
}
