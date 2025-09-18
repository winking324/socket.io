// Package emitter provides a broadcast operator for emitting events to Socket.IO clients via Redis.
package emitter

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/winking324/socket.io/adapters/adapter/v3"
	"github.com/winking324/socket.io/adapters/redis/v3"
	"github.com/winking324/socket.io/parsers/socket/v3/parser"
	"github.com/winking324/socket.io/servers/socket/v3"
	"github.com/winking324/socket.io/v3/pkg/types"
)

// RESERVED_EVENTS is a set of event names that are reserved and cannot be emitted by the user.
var RESERVED_EVENTS = types.NewSet(
	"connect",
	"connect_error",
	"disconnect",
	"disconnecting",
	"newListener",
	"removeListener",
)

// BroadcastOperator allows targeting, excluding, and flagging rooms for event emission via Redis.
type BroadcastOperator struct {
	redisClient      *redis.RedisClient      // Redis client for publishing messages.
	broadcastOptions *BroadcastOptions       // Options for broadcasting.
	rooms            *types.Set[socket.Room] // Targeted rooms.
	exceptRooms      *types.Set[socket.Room] // Rooms to exclude.
	flags            *socket.BroadcastFlags  // Broadcast flags (e.g., compress, volatile).
}

// MakeBroadcastOperator creates a new BroadcastOperator with default values.
func MakeBroadcastOperator() *BroadcastOperator {
	b := &BroadcastOperator{
		rooms:       types.NewSet[socket.Room](),
		exceptRooms: types.NewSet[socket.Room](),
		flags:       &socket.BroadcastFlags{},
	}

	return b
}

// NewBroadcastOperator creates and initializes a new BroadcastOperator.
func NewBroadcastOperator(redisClient *redis.RedisClient, broadcastOptions *BroadcastOptions, rooms *types.Set[socket.Room], exceptRooms *types.Set[socket.Room], flags *socket.BroadcastFlags) *BroadcastOperator {
	b := MakeBroadcastOperator()

	b.Construct(redisClient, broadcastOptions, rooms, exceptRooms, flags)

	return b
}

// Construct initializes the BroadcastOperator with the given parameters.
func (b *BroadcastOperator) Construct(redisClient *redis.RedisClient, broadcastOptions *BroadcastOptions, rooms *types.Set[socket.Room], exceptRooms *types.Set[socket.Room], flags *socket.BroadcastFlags) {
	b.redisClient = redisClient

	if broadcastOptions == nil {
		broadcastOptions = &BroadcastOptions{}
	}
	b.broadcastOptions = broadcastOptions

	if rooms != nil {
		b.rooms = rooms
	}
	if exceptRooms != nil {
		b.exceptRooms = exceptRooms
	}
	if flags != nil {
		b.flags = flags
	}
}

// To targets one or more rooms for event emission.
func (b *BroadcastOperator) To(room ...socket.Room) *BroadcastOperator {
	rooms := types.NewSet(b.rooms.Keys()...)
	rooms.Add(room...)
	return NewBroadcastOperator(b.redisClient, b.broadcastOptions, rooms, b.exceptRooms, b.flags)
}

// In is an alias for To, targeting one or more rooms for event emission.
func (b *BroadcastOperator) In(room ...socket.Room) *BroadcastOperator {
	return b.To(room...)
}

// Except excludes one or more rooms from event emission.
func (b *BroadcastOperator) Except(room ...socket.Room) *BroadcastOperator {
	exceptRooms := types.NewSet(b.exceptRooms.Keys()...)
	exceptRooms.Add(room...)
	return NewBroadcastOperator(b.redisClient, b.broadcastOptions, b.rooms, exceptRooms, b.flags)
}

// Compress sets the compress flag for the broadcast.
func (b *BroadcastOperator) Compress(compress bool) *BroadcastOperator {
	flags := *b.flags
	flags.Compress = &compress
	return NewBroadcastOperator(b.redisClient, b.broadcastOptions, b.rooms, b.exceptRooms, &flags)
}

// Volatile sets the volatile flag, allowing event data to be lost if the client is not ready.
func (b *BroadcastOperator) Volatile() *BroadcastOperator {
	flags := *b.flags
	flags.Volatile = true
	return NewBroadcastOperator(b.redisClient, b.broadcastOptions, b.rooms, b.exceptRooms, &flags)
}

// Emit emits an event to all targeted clients, except reserved events.
func (b *BroadcastOperator) Emit(ev string, args ...any) error {
	if RESERVED_EVENTS.Has(ev) {
		return fmt.Errorf(`"%s" is a reserved event name`, ev)
	}

	if b.broadcastOptions.Parser == nil {
		return errors.New(`broadcastOptions.Parser is not set`)
	}

	// set up packet object
	data := append([]any{ev}, args...)

	packet := &parser.Packet{
		Type: parser.EVENT,
		Nsp:  b.broadcastOptions.Nsp,
		Data: data,
	}

	opts := &adapter.PacketOptions{
		Rooms:  b.rooms.Keys(),
		Except: b.exceptRooms.Keys(),
		Flags:  b.flags,
	}

	msg, err := b.broadcastOptions.Parser.Encode(&Packet{
		Uid:    UID,
		Packet: packet,
		Opts:   opts,
	})
	if err != nil {
		return err
	}

	channel := b.broadcastOptions.BroadcastChannel
	if b.rooms != nil && b.rooms.Len() == 1 {
		for _, room := range b.rooms.Keys() {
			channel += string(room) + "#"
			break // Only need the first room since there's exactly one
		}
	}

	emitter_log.Debug("publishing message to channel %s", channel)

	return b.redisClient.Client.Publish(b.redisClient.Context, channel, msg).Err()
}

// SocketsJoin makes the matching socket instances join the specified rooms.
func (b *BroadcastOperator) SocketsJoin(rooms ...socket.Room) error {
	request, err := json.Marshal(&Request{
		Type: redis.REMOTE_JOIN,
		Opts: &adapter.PacketOptions{
			Rooms:  b.rooms.Keys(),
			Except: b.exceptRooms.Keys(),
		},
		Rooms: rooms,
	})
	if err != nil {
		return err
	}

	return b.redisClient.Client.Publish(b.redisClient.Context, b.broadcastOptions.RequestChannel, request).Err()
}

// SocketsLeave makes the matching socket instances leave the specified rooms.
func (b *BroadcastOperator) SocketsLeave(rooms ...socket.Room) error {
	request, err := json.Marshal(&Request{
		Type: redis.REMOTE_LEAVE,
		Opts: &adapter.PacketOptions{
			Rooms:  b.rooms.Keys(),
			Except: b.exceptRooms.Keys(),
		},
		Rooms: rooms,
	})
	if err != nil {
		return err
	}

	return b.redisClient.Client.Publish(b.redisClient.Context, b.broadcastOptions.RequestChannel, request).Err()
}

// DisconnectSockets disconnects the matching socket instances.
func (b *BroadcastOperator) DisconnectSockets(state bool) error {
	request, err := json.Marshal(&Request{
		Type: redis.REMOTE_DISCONNECT,
		Opts: &adapter.PacketOptions{
			Rooms:  b.rooms.Keys(),
			Except: b.exceptRooms.Keys(),
		},
		Close: state,
	})
	if err != nil {
		return err
	}

	return b.redisClient.Client.Publish(b.redisClient.Context, b.broadcastOptions.RequestChannel, request).Err()
}
