package parser

import (
	"github.com/winking324/socket.io/v3/pkg/types"
)

// Encoder defines an interface for socket.io encoding.
type (
	Encoder interface {
		Encode(*Packet) []types.BufferInterface
	}

	// Decoder defines an interface for socket.io decoding.
	Decoder interface {
		types.EventEmitter

		Add(any) error
		Destroy()
	}

	// Parser defines an interface for creating Encoder and Decoder instances.
	Parser interface {
		NewEncoder() Encoder
		NewDecoder() Decoder
	}

	// parser implements the Parser interface.
	parser struct{}
)

// NewEncoder creates a new Encoder instance.
func (p *parser) NewEncoder() Encoder {
	return NewEncoder()
}

// NewDecoder creates a new Decoder instance.
func (p *parser) NewDecoder() Decoder {
	return NewDecoder()
}

// NewParser creates a new Parser instance.
func NewParser() Parser {
	return &parser{}
}
