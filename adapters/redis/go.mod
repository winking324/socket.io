module github.com/winking324/socket.io/adapters/redis/v3

go 1.24.1

require (
	github.com/redis/go-redis/v9 v9.17.3
	github.com/vmihailenco/msgpack/v5 v5.4.1
	github.com/winking324/socket.io/adapters/adapter/v3 v3.0.0-rc.13
	github.com/winking324/socket.io/parsers/socket/v3 v3.0.0-rc.13
	github.com/winking324/socket.io/servers/socket/v3 v3.0.0-rc.13
	github.com/winking324/socket.io/v3 v3.0.0-rc.13
)

require (
	github.com/andybalholm/brotli v1.2.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/dunglas/httpsfv v1.1.0 // indirect
	github.com/gookit/color v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/klauspost/compress v1.18.4 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/quic-go/quic-go v0.59.0 // indirect
	github.com/quic-go/webtransport-go v0.10.0 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/winking324/socket.io/parsers/engine/v3 v3.0.0-rc.13 // indirect
	github.com/winking324/socket.io/servers/engine/v3 v3.0.0-rc.13 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/net v0.50.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/text v0.34.0 // indirect
)

replace (
	github.com/winking324/socket.io/adapters/adapter/v3 => ../../adapters/adapter
	github.com/winking324/socket.io/parsers/engine/v3 => ../../parsers/engine
	github.com/winking324/socket.io/parsers/socket/v3 => ../../parsers/socket
	github.com/winking324/socket.io/servers/engine/v3 => ../../servers/engine
	github.com/winking324/socket.io/servers/socket/v3 => ../../servers/socket
	github.com/winking324/socket.io/v3 => ../../
)
