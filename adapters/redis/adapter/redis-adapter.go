package adapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zishang520/socket.io/v3/pkg/log"
	"github.com/zishang520/socket.io/v3/pkg/utils"
	"github.com/zishang520/socket.io/parsers/socket/v3/parser"
	"github.com/zishang520/socket.io/adapters/redis/v3/types"
	"github.com/zishang520/socket.io/adapters/adapter/v3"
	"github.com/zishang520/socket.io/servers/socket/v3"
)

var redis_log = log.NewLog("socket.io-redis")

const (
	PSUB string = "psub"
	SUB  string = "sub"
)

type (
	RedisAdapterBuilder struct {
		// a Redis client
		Redis *types.RedisClient
		// additional options
		Opts RedisAdapterOptionsInterface
	}

	redisAdapter struct {
		socket.Adapter

		redisClient *types.RedisClient
		opts        *RedisAdapterOptions

		uid                              adapter.ServerId
		requestsTimeout                  time.Duration
		publishOnSpecificResponseChannel bool
		parser                           types.Parser

		channel                 string
		requestChannel          string
		responseChannel         string
		specificResponseChannel string

		requests             *types.Map[string, *RedisRequest]
		ackRequests          *types.Map[string, *AckRequest]
		redisListeners       *types.Map[string, *redis.PubSub]
		friendlyErrorHandler func(...any)
	}
)

// Adapter constructor.
func (rb *RedisAdapterBuilder) New(nsp socket.Namespace) socket.Adapter {
	return NewRedisAdapter(nsp, rb.Redis, rb.Opts)
}

func MakeRedisAdapter() RedisAdapter {
	c := &redisAdapter{
		Adapter: socket.MakeAdapter(),

		opts:                 DefaultRedisAdapterOptions(),
		requests:             &types.Map[string, *RedisRequest]{},
		ackRequests:          &types.Map[string, *AckRequest]{},
		redisListeners:       &types.Map[string, *redis.PubSub]{},
		friendlyErrorHandler: func(...any) {},
	}

	c.Prototype(c)

	return c
}

func NewRedisAdapter(nsp socket.Namespace, redis *types.RedisClient, opts any) RedisAdapter {
	c := MakeRedisAdapter()

	c.SetRedis(redis)
	c.SetOpts(opts)

	c.Construct(nsp)

	return c
}

func (r *redisAdapter) SetRedis(redis *types.RedisClient) {
	r.redisClient = redis
}

func (r *redisAdapter) SetOpts(opts any) {
	if options, ok := opts.(RedisAdapterOptionsInterface); ok {
		r.opts.Assign(options)
	}
}

// readonly
func (r *redisAdapter) Uid() adapter.ServerId {
	return r.uid
}

// readonly
func (r *redisAdapter) RequestsTimeout() time.Duration {
	return r.requestsTimeout
}

// readonly
func (r *redisAdapter) PublishOnSpecificResponseChannel() bool {
	return r.publishOnSpecificResponseChannel
}

// readonly
func (r *redisAdapter) Parser() types.Parser {
	return r.parser
}

// Adapter constructor.
func (r *redisAdapter) Construct(nsp socket.Namespace) {
	r.Adapter.Construct(nsp)

	uid, _ := adapter.Uid2(6)
	r.uid = adapter.ServerId(uid)
	if r.opts.GetRawRequestsTimeout() != nil {
		r.requestsTimeout = r.opts.RequestsTimeout()
	} else {
		r.requestsTimeout = 5_000 * time.Millisecond
	}
	r.publishOnSpecificResponseChannel = r.opts.PublishOnSpecificResponseChannel()
	if r.opts.GetRawParser() != nil {
		r.parser = r.opts.Parser()
	} else {
		r.parser = utils.MsgPack()
	}

	prefix := "socket.io"
	if r.opts.GetRawKey() != nil {
		prefix = r.opts.Key()
	}

	r.channel = prefix + "#" + nsp.Name() + "#"
	r.requestChannel = prefix + "-request#" + r.Nsp().Name() + "#"
	r.responseChannel = prefix + "-response#" + r.Nsp().Name() + "#"
	r.specificResponseChannel = r.responseChannel + string(r.uid) + "#"

	r.friendlyErrorHandler = func(...any) {
		if r.redisClient.ListenerCount("error") == 1 {
			redis_log.Warning("missing 'error' handler on this Redis client")
		}
	}

	r.redisClient.On("error", r.friendlyErrorHandler)

	pubsub := r.redisClient.Client.PSubscribe(r.redisClient.Context, r.channel+"*")
	r.redisListeners.Store(PSUB, pubsub)
	go func() {
		defer pubsub.Close()

		for {
			select {
			case <-r.redisClient.Context.Done():
				return
			default:
				msg, err := pubsub.ReceiveMessage(r.redisClient.Context)
				if err != nil {
					r.redisClient.Emit("error", err)
					if err == redis.ErrClosed {
						return
					}
					continue // retry receiving messages
				}
				r.onMessage(msg.Pattern, msg.Channel, []byte(msg.Payload))
			}
		}
	}()

	sub := r.redisClient.Client.Subscribe(r.redisClient.Context, r.requestChannel, r.responseChannel, r.specificResponseChannel)
	r.redisListeners.Store(SUB, sub)
	go func() {
		defer sub.Close()

		for {
			select {
			case <-r.redisClient.Context.Done():
				return
			default:
				msg, err := sub.ReceiveMessage(r.redisClient.Context)
				if err != nil {
					r.redisClient.Emit("error", err)
					if err == redis.ErrClosed {
						return
					}
					continue // retry receiving messages
				}
				r.onRequest(msg.Channel, []byte(msg.Payload))
			}
		}
	}()
}

// Called with a subscription message
func (r *redisAdapter) onMessage(pattern string, channel string, msg []byte) {
	if len(channel) == 0 || len(channel) <= len(r.channel) {
		redis_log.Debug("ignore channel shorter than expected")
		return
	}

	if !strings.HasPrefix(channel, r.channel) {
		redis_log.Debug("ignore different channel")
		return
	}

	room := channel[len(r.channel) : len(channel)-1]
	if room != "" && !r.hasRoom(socket.Room(room)) {
		redis_log.Debug("ignore unknown room %s", room)
		return
	}

	var packet *Packet
	if err := r.parser.Decode(msg, &packet); err != nil {
		redis_log.Debug("error decoding message: %v", err)
		return
	}

	if r.uid == packet.Uid {
		redis_log.Debug("ignore same uid")
		return
	}

	if packet.Packet != nil && packet.Packet.Nsp == "" {
		packet.Packet.Nsp = "/"
	}

	if packet.Packet == nil || packet.Packet.Nsp != r.Nsp().Name() {
		redis_log.Debug("ignore different namespace")
		return
	}

	r.Adapter.Broadcast(packet.Packet, adapter.DecodeOptions(packet.Opts))
}

func (r *redisAdapter) hasRoom(room socket.Room) bool {
	_, ok := r.Rooms().Load(room)
	return ok
}

// Called on request from another node
func (r *redisAdapter) onRequest(channel string, msg []byte) {
	if strings.HasPrefix(channel, r.responseChannel) {
		r.onResponse(channel, msg)
		return
	} else if !strings.HasPrefix(channel, r.requestChannel) {
		redis_log.Debug("ignore different channel")
		return
	}

	var request *Request
	// if the buffer starts with a "{" character
	if msg[0] == '{' {
		if err := json.Unmarshal(msg, &request); err != nil {
			redis_log.Debug("ignoring malformed request")
			return
		}
	} else {
		if err := r.parser.Decode(msg, &request); err != nil {
			redis_log.Debug("ignoring malformed request")
			return
		}
	}

	redis_log.Debug("received request %v", request)

	switch request.Type {
	case types.SOCKETS: // No business code related to this message was found.
		if _, ok := r.requests.Load(request.RequestId); ok {
			return
		}

		sockets := r.Adapter.Sockets(types.NewSet(request.Rooms...))

		response, err := json.Marshal(&Response{
			RequestId: request.RequestId,
			Sockets: adapter.SliceMap(sockets.Keys(), func(socketId socket.SocketId) *adapter.SocketResponse {
				return &adapter.SocketResponse{
					Id: socketId,
				}
			}),
		})
		if err != nil {
			redis_log.Debug("Error marshaling SOCKETS response for RequestId %s: %s", request.RequestId, err.Error())
			return
		}

		r.publishResponse(request, response)

	case types.ALL_ROOMS:
		if _, ok := r.requests.Load(request.RequestId); ok {
			return
		}

		response, err := json.Marshal(&Response{
			RequestId: request.RequestId,
			Rooms:     r.Rooms().Keys(),
		})
		if err != nil {
			redis_log.Debug("Error marshaling ALL_ROOMS response for RequestId %s: %s", request.RequestId, err.Error())
			return
		}

		r.publishResponse(request, response)

	case types.REMOTE_JOIN:
		if request.Opts != nil {
			r.Adapter.AddSockets(adapter.DecodeOptions(request.Opts), request.Rooms)
			return
		}

		if client, ok := r.Nsp().Sockets().Load(request.Sid); ok {
			client.Join(request.Room)

			response, err := json.Marshal(&Response{
				RequestId: request.RequestId,
			})
			if err != nil {
				redis_log.Debug("Error marshaling REMOTE_JOIN response for RequestId %s: %s", request.RequestId, err.Error())
				return
			}
			r.publishResponse(request, response)
		}

	case types.REMOTE_LEAVE:
		if request.Opts != nil {
			r.Adapter.DelSockets(adapter.DecodeOptions(request.Opts), request.Rooms)
			return
		}

		if client, ok := r.Nsp().Sockets().Load(request.Sid); ok {
			client.Leave(request.Room)

			response, err := json.Marshal(&Response{
				RequestId: request.RequestId,
			})
			if err != nil {
				redis_log.Debug("Error marshaling REMOTE_LEAVE response for RequestId %s: %s", request.RequestId, err.Error())
				return
			}
			r.publishResponse(request, response)
		}

	case types.REMOTE_DISCONNECT:
		if request.Opts != nil {
			r.Adapter.DisconnectSockets(adapter.DecodeOptions(request.Opts), request.Close)
			return
		}

		if client, ok := r.Nsp().Sockets().Load(request.Sid); ok {
			client.Disconnect(request.Close)

			response, err := json.Marshal(&Response{
				RequestId: request.RequestId,
			})
			if err != nil {
				redis_log.Debug("Error marshaling REMOTE_DISCONNECT response for RequestId %s: %s", request.RequestId, err.Error())
				return
			}
			r.publishResponse(request, response)
		}

	case types.REMOTE_FETCH:
		if _, ok := r.requests.Load(request.RequestId); ok {
			return
		}
		r.Adapter.FetchSockets(adapter.DecodeOptions(request.Opts))(func(localSockets []socket.SocketDetails, e error) {
			if e != nil {
				redis_log.Debug("REMOTE_FETCH Adapter.FetchSockets error: %s", e.Error())
				return
			}
			response, err := json.Marshal(&Response{
				RequestId: request.RequestId,
				Sockets: adapter.SliceMap(localSockets, func(client socket.SocketDetails) *adapter.SocketResponse {
					return &adapter.SocketResponse{
						Id:        client.Id(),
						Handshake: client.Handshake(),
						Rooms:     client.Rooms().Keys(),
						Data:      client.Data(),
					}
				}),
			})
			if err != nil {
				redis_log.Debug("Error marshaling REMOTE_FETCH response for RequestId %s: %s", request.RequestId, err.Error())
				return
			}
			r.publishResponse(request, response)
		})

	case types.SERVER_SIDE_EMIT:
		if request.Uid == r.uid {
			redis_log.Debug("ignore same uid")
			return
		}
		if request.RequestId == "" {
			r.Nsp().OnServerSideEmit(request.Data)
			return
		}
		called := sync.Once{}
		callback := socket.Ack(func(args []any, err error) {
			// only one argument is expected
			called.Do(func() {
				redis_log.Debug("calling acknowledgement with %v", args)
				response, err := json.Marshal(&Response{
					Type:      types.SERVER_SIDE_EMIT,
					RequestId: request.RequestId,
					Data:      args,
				})
				if err != nil {
					redis_log.Debug("Error marshaling SERVER_SIDE_EMIT response for RequestId %s: %s", request.RequestId, err.Error())
					return
				}
				if err := r.redisClient.Client.Publish(r.redisClient.Context, r.responseChannel, response).Err(); err != nil {
					r.redisClient.Emit("error", err)
				}
			})
		})
		r.Nsp().OnServerSideEmit(append(request.Data, callback))

	case types.BROADCAST:
		if _, ok := r.ackRequests.Load(request.RequestId); ok {
			// ignore self
			return
		}

		r.Adapter.BroadcastWithAck(
			request.Packet,
			adapter.DecodeOptions(request.Opts),
			func(clientCount uint64) {
				redis_log.Debug("waiting for %d client acknowledgements", clientCount)
				response, err := json.Marshal(&Response{
					Type:        types.BROADCAST_CLIENT_COUNT,
					RequestId:   request.RequestId,
					ClientCount: clientCount,
				})
				if err != nil {
					redis_log.Debug("Error marshaling BROADCAST_CLIENT_COUNT response for RequestId %s: %s", request.RequestId, err.Error())
					return
				}
				r.publishResponse(request, response)
			},
			func(args []any, _ error) {
				redis_log.Debug("received acknowledgement with value %v", args)
				response, err := r.parser.Encode(&Response{
					Type:      types.BROADCAST_ACK,
					RequestId: request.RequestId,
					Packet:    args,
				})
				if err != nil {
					redis_log.Debug("Error marshaling BROADCAST_ACK response for RequestId %s: %s", request.RequestId, err.Error())
					return
				}
				r.publishResponse(request, response)
			},
		)

	default:
		redis_log.Debug("ignoring unknown request type: %d", request.Type)
	}
}

// Send the response to the requesting node
func (r *redisAdapter) publishResponse(request *Request, response []byte) {
	responseChannel := r.responseChannel
	if r.publishOnSpecificResponseChannel {
		responseChannel += string(request.Uid) + "#"
	}
	redis_log.Debug("publishing response to channel %s", responseChannel)
	if err := r.redisClient.Client.Publish(r.redisClient.Context, responseChannel, response).Err(); err != nil {
		r.redisClient.Emit("error", err)
	}
}

// Called on response from another node
func (r *redisAdapter) onResponse(channel string, msg []byte) {
	var response *Response

	// if the buffer starts with a "{" character
	if msg[0] == '{' {
		if err := json.Unmarshal(msg, &response); err != nil {
			redis_log.Debug("ignoring malformed response")
			return
		}
	} else {
		if err := r.parser.Decode(msg, &response); err != nil {
			redis_log.Debug("ignoring malformed response")
			return
		}
	}

	requestId := response.RequestId

	if ackRequest, ok := r.ackRequests.Load(requestId); ok {
		switch response.Type {
		case types.BROADCAST_CLIENT_COUNT:
			ackRequest.ClientCountCallback(response.ClientCount)

		case types.BROADCAST_ACK:
			ackRequest.Ack(response.Packet, nil)

		}
		return
	}

	if requestId == "" {
		redis_log.Debug("ignoring unknown request")
		return
	} else if request, ok := r.requests.Load(requestId); !ok {
		redis_log.Debug("ignoring unknown request")
		return
	} else {
		redis_log.Debug("received response %v", response)
		switch request.Type {
		case types.SOCKETS, types.REMOTE_FETCH:
			request.MsgCount.Add(1)

			// ignore if response does not contain 'sockets' key
			if response.Sockets == nil {
				return
			}
			request.Sockets.Push(response.Sockets...)

			if request.MsgCount.Load() == request.NumSub {
				utils.ClearTimeout(request.Timeout.Load())
				if request.Resolve != nil {
					request.Resolve(types.NewSlice(adapter.SliceMap(request.Sockets.All(), func(client *adapter.SocketResponse) any {
						return socket.SocketDetails(adapter.NewRemoteSocket(client))
					})...))
				}
				r.requests.Delete(requestId)
			}

		case types.ALL_ROOMS:
			request.MsgCount.Add(1)

			// ignore if response does not contain 'rooms' key
			if response.Rooms == nil {
				return
			}
			request.Rooms.Add(response.Rooms...)

			if request.MsgCount.Load() == request.NumSub {
				utils.ClearTimeout(request.Timeout.Load())
				if request.Resolve != nil {
					request.Resolve(types.NewSlice(adapter.SliceMap(request.Rooms.Keys(), func(room socket.Room) any {
						return room
					})...))
				}
				r.requests.Delete(requestId)
			}

		case types.REMOTE_JOIN, types.REMOTE_LEAVE, types.REMOTE_DISCONNECT:
			utils.ClearTimeout(request.Timeout.Load())
			if request.Resolve != nil {
				request.Resolve(nil)
			}
			r.requests.Delete(requestId)
			break

		case types.SERVER_SIDE_EMIT:
			request.Responses.Push(response.Data)

			redis_log.Debug("serverSideEmit: got %d responses out of %d", request.Responses.Len(), request.NumSub)
			if int64(request.Responses.Len()) == request.NumSub {
				utils.ClearTimeout(request.Timeout.Load())
				if request.Resolve != nil {
					request.Resolve(request.Responses)
				}
				r.requests.Delete(requestId)
			}

		default:
			redis_log.Debug("ignoring unknown request type: %d", request.Type)
		}
	}
}

// Broadcasts a packet.
func (r *redisAdapter) Broadcast(packet *parser.Packet, opts *socket.BroadcastOptions) {
	packet.Nsp = r.Nsp().Name()

	onlyLocal := opts != nil && opts.Flags != nil && opts.Flags.Local

	if !onlyLocal {
		if msg, err := r.parser.Encode(&Packet{
			Uid:    r.Uid(),
			Packet: packet,
			Opts:   adapter.EncodeOptions(opts),
		}); err == nil {
			channel := r.channel
			if opts.Rooms != nil && opts.Rooms.Len() == 1 {
				for _, room := range opts.Rooms.Keys() {
					channel += string(room) + "#"
					break // Only need the first room since there's exactly one
				}
			}
			redis_log.Debug("publishing message to channel %s", channel)
			if err := r.redisClient.Client.Publish(r.redisClient.Context, channel, msg).Err(); err != nil {
				r.redisClient.Emit("error", err)
			}
		}
	}
	r.Adapter.Broadcast(packet, opts)
}

func (r *redisAdapter) BroadcastWithAck(packet *parser.Packet, opts *socket.BroadcastOptions, clientCountCallback func(uint64), ack socket.Ack) {
	packet.Nsp = r.Nsp().Name()

	onlyLocal := opts != nil && opts.Flags != nil && opts.Flags.Local

	if !onlyLocal {
		// TODO: How to handle err???
		if requestId, err := adapter.Uid2(6); err == nil {
			if request, err := r.parser.Encode(&Request{
				Uid:       r.uid,
				RequestId: requestId,
				Type:      types.BROADCAST,
				Packet:    packet,
				Opts:      adapter.EncodeOptions(opts),
			}); err == nil {
				if err := r.redisClient.Client.Publish(r.redisClient.Context, r.requestChannel, request).Err(); err != nil {
					r.redisClient.Emit("error", err)
				}

				r.ackRequests.Store(requestId, &AckRequest{
					ClientCountCallback: clientCountCallback,
					Ack:                 ack,
				})

				t := time.Duration(0)
				if opts != nil && opts.Flags != nil && opts.Flags.Timeout != nil {
					t = *opts.Flags.Timeout
				}
				// we have no way to know at this level whether the server has received an acknowledgement from each client, so we
				// will simply clean up the ackRequests map after the given delay
				utils.SetTimeout(func() {
					r.ackRequests.Delete(requestId)
				}, t)
			}
		}
	}

	r.Adapter.BroadcastWithAck(packet, opts, clientCountCallback, ack)
}

// Gets the list of all rooms (across every node)
func (r *redisAdapter) AllRooms() func(func(*types.Set[socket.Room], error)) {
	return func(cb func(*types.Set[socket.Room], error)) {
		localRooms := types.NewSet(r.Rooms().Keys()...)
		numSub := r.ServerCount()
		redis_log.Debug(`waiting for %d responses to "allRooms" request`, numSub)

		if numSub <= 1 {
			cb(localRooms, nil)
			return
		}

		requestId, err := adapter.Uid2(6)
		if err != nil {
			cb(nil, err)
			return
		}
		request, err := json.Marshal(&Request{
			Type:      types.ALL_ROOMS,
			Uid:       r.uid,
			RequestId: requestId,
		})
		if err != nil {
			cb(nil, err)
			return
		}

		timeout := utils.SetTimeout(func() {
			if _, ok := r.requests.Load(requestId); ok {
				cb(nil, errors.New("timeout reached while waiting for allRooms response"))
				r.requests.Delete(requestId)
			}
		}, r.requestsTimeout)

		r.requests.Store(requestId, &RedisRequest{
			Type:   types.ALL_ROOMS,
			NumSub: numSub,
			Resolve: func(data *types.Slice[any]) {
				cb(types.NewSet(adapter.SliceMap(data.All(), func(room any) socket.Room {
					return room.(socket.Room)
				})...), nil)
			},
			Timeout: adapter.Tap(&atomic.Pointer[utils.Timer]{}, func(t *atomic.Pointer[utils.Timer]) {
				t.Store(timeout)
			}),
			MsgCount: adapter.Tap(&atomic.Int64{}, func(c *atomic.Int64) {
				c.Store(1)
			}),
			Rooms: localRooms,
		})

		if err := r.redisClient.Client.Publish(r.redisClient.Context, r.requestChannel, request).Err(); err != nil {
			r.redisClient.Emit("error", err)
		}
	}
}

func (r *redisAdapter) FetchSockets(opts *socket.BroadcastOptions) func(func([]socket.SocketDetails, error)) {
	return func(cb func([]socket.SocketDetails, error)) {
		r.Adapter.FetchSockets(opts)(func(localSockets []socket.SocketDetails, _ error) {
			if opts.Flags != nil && opts.Flags.Local {
				cb(localSockets, nil)
				return
			}

			numSub := r.ServerCount()
			redis_log.Debug(`waiting for %d responses to "fetchSockets" request`, numSub)

			if numSub <= 1 {
				cb(localSockets, nil)
				return
			}

			requestId, err := adapter.Uid2(6)
			if err != nil {
				cb(nil, err)
				return
			}

			request, err := json.Marshal(&Request{
				Type:      types.REMOTE_FETCH,
				Uid:       r.uid,
				RequestId: requestId,
				Opts:      adapter.EncodeOptions(opts),
			})
			if err == nil {
				cb(nil, err)
				return
			}

			timeout := utils.SetTimeout(func() {
				if _, ok := r.requests.Load(requestId); ok {
					cb(nil, errors.New("timeout reached while waiting for fetchSockets response"))
					r.requests.Delete(requestId)
				}
			}, r.requestsTimeout)

			r.requests.Store(requestId, &RedisRequest{
				Type:   types.REMOTE_FETCH,
				NumSub: numSub,
				Resolve: func(data *types.Slice[any]) {
					cb(adapter.SliceMap(data.All(), func(i any) socket.SocketDetails {
						return i.(socket.SocketDetails)
					}), nil)
				},
				Timeout: adapter.Tap(&atomic.Pointer[utils.Timer]{}, func(t *atomic.Pointer[utils.Timer]) {
					t.Store(timeout)
				}),
				MsgCount: adapter.Tap(&atomic.Int64{}, func(c *atomic.Int64) {
					c.Store(1)
				}),
				Sockets: types.NewSlice(adapter.SliceMap(localSockets, func(client socket.SocketDetails) *adapter.SocketResponse {
					return &adapter.SocketResponse{
						Id:        client.Id(),
						Handshake: client.Handshake(),
						Rooms:     client.Rooms().Keys(),
						Data:      client.Data(),
					}
				})...),
			})

			if err := r.redisClient.Client.Publish(r.redisClient.Context, r.requestChannel, request).Err(); err != nil {
				r.redisClient.Emit("error", err)
			}
		})
	}
}

func (r *redisAdapter) AddSockets(opts *socket.BroadcastOptions, rooms []socket.Room) {
	if opts != nil && opts.Flags != nil && opts.Flags.Local {
		r.Adapter.AddSockets(opts, rooms)
		return
	}

	if request, err := json.Marshal(&Request{
		Uid:   r.uid,
		Type:  types.REMOTE_JOIN,
		Opts:  adapter.EncodeOptions(opts),
		Rooms: rooms,
	}); err == nil {
		if err := r.redisClient.Client.Publish(r.redisClient.Context, r.requestChannel, request).Err(); err != nil {
			r.redisClient.Emit("error", err)
		}
	}
}

func (r *redisAdapter) DelSockets(opts *socket.BroadcastOptions, rooms []socket.Room) {
	if opts != nil && opts.Flags != nil && opts.Flags.Local {
		r.Adapter.DelSockets(opts, rooms)
		return
	}

	if request, err := json.Marshal(&Request{
		Uid:   r.uid,
		Type:  types.REMOTE_LEAVE,
		Opts:  adapter.EncodeOptions(opts),
		Rooms: rooms,
	}); err == nil {
		if err := r.redisClient.Client.Publish(r.redisClient.Context, r.requestChannel, request).Err(); err != nil {
			r.redisClient.Emit("error", err)
		}
	}
}

func (r *redisAdapter) DisconnectSockets(opts *socket.BroadcastOptions, state bool) {
	if opts != nil && opts.Flags != nil && opts.Flags.Local {
		r.Adapter.DisconnectSockets(opts, state)
		return
	}

	if request, err := json.Marshal(&Request{
		Uid:   r.uid,
		Type:  types.REMOTE_DISCONNECT,
		Opts:  adapter.EncodeOptions(opts),
		Close: state,
	}); err == nil {
		if err := r.redisClient.Client.Publish(r.redisClient.Context, r.requestChannel, request).Err(); err != nil {
			r.redisClient.Emit("error", err)
		}
	}
}

func (r *redisAdapter) ServerSideEmit(packet []any) error {
	if len(packet) == 0 {
		return fmt.Errorf("packet cannot be empty")
	}

	if ack, withAck := packet[len(packet)-1].(socket.Ack); withAck {
		return r.serverSideEmitWithAck(packet[:len(packet)-1], ack)
	}

	request, err := json.Marshal(&Request{
		Uid:  r.uid,
		Type: types.SERVER_SIDE_EMIT,
		Data: packet,
	})

	if err != nil {
		return err
	}
	return r.redisClient.Client.Publish(r.redisClient.Context, r.requestChannel, request).Err()
}

func (r *redisAdapter) serverSideEmitWithAck(packet []any, ack socket.Ack) error {
	numSub := r.ServerCount() - 1 // ignore self

	redis_log.Debug(`waiting for %d responses to "serverSideEmit" request`, numSub)

	if numSub <= 0 {
		ack(nil, nil)
		return nil
	}

	requestId, err := adapter.Uid2(6)
	if err != nil {
		return err
	}

	request, err := json.Marshal(&Request{
		Uid:       r.uid,
		RequestId: requestId, // the presence of this attribute defines whether an acknowledgement is needed
		Type:      types.SERVER_SIDE_EMIT,
		Data:      packet,
	})
	if err != nil {
		return err
	}

	timeout := utils.SetTimeout(func() {
		if storedRequest, ok := r.requests.Load(requestId); ok {
			ack(storedRequest.Responses.All(), errors.New(fmt.Sprintf(`timeout reached: only %d responses received out of %d`, storedRequest.Responses.Len(), storedRequest.NumSub)))
			r.requests.Delete(requestId)
		}
	}, r.requestsTimeout)

	r.requests.Store(requestId, &RedisRequest{
		Type:   types.SERVER_SIDE_EMIT,
		NumSub: numSub,
		Timeout: adapter.Tap(&atomic.Pointer[utils.Timer]{}, func(t *atomic.Pointer[utils.Timer]) {
			t.Store(timeout)
		}),
		Resolve: func(data *types.Slice[any]) {
			ack(data.All(), nil)
		},
		Responses: types.NewSlice[any](),
	})

	return r.redisClient.Client.Publish(r.redisClient.Context, r.requestChannel, request).Err()
}

func (r *redisAdapter) ServerCount() int64 {
	result, err := r.redisClient.Client.PubSubNumSub(r.redisClient.Context, r.requestChannel).Result()
	if err != nil {
		r.redisClient.Emit("error", err)
		return 0
	}

	if count, ok := result[r.requestChannel]; ok {
		return count
	}
	return 0
}

func (r *redisAdapter) Close() {
	if psub, ok := r.redisListeners.Load(PSUB); ok {
		if err := psub.PUnsubscribe(r.redisClient.Context, r.channel+"*"); err != nil {
			r.redisClient.Emit("error", err)
		}
	}
	if sub, ok := r.redisListeners.Load(SUB); ok {
		if err := sub.Unsubscribe(r.redisClient.Context, r.requestChannel, r.responseChannel, r.specificResponseChannel); err != nil {
			r.redisClient.Emit("error", err)
		}
	}
	// Thinking about whether r.redisListeners needs to be cleared?
	r.redisClient.RemoveListener("error", r.friendlyErrorHandler)
}
