// Copyright 2015 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package rpc

import (
	"context"
	"net"
	"reflect"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/p2p/netutil"
)

// ServeListener accepts connections on l, serving JSON-RPC on them.
func (s *Server) ServeProtoListener(l net.Listener) error {
	for {
		conn, err := l.Accept()
		if netutil.IsTemporaryError(err) {
			log.Warn("ProtoRPC accept error", "err", err)
			continue
		} else if err != nil {
			return err
		}
		log.Warn("Accepted ProtoRPC connection", "conn", conn.RemoteAddr())
		go s.ServeProtoCodec(NewProtoCodec(conn), 0)
	}
}

// ServeCodec reads incoming requests from codec, calls the appropriate callback and writes
// the response back using the given codec. It will block until the codec is closed or the
// server is stopped. In either case the codec is closed.
//
// Note that codec options are no longer supported.
func (s *Server) ServeProtoCodec(codec *protobufCodec, options CodecOption) {
	defer codec.close()

	c := initProtoClient(codec, s.idgen, &s.services)
	<-codec.closed()
	c.Close()
}

// Client represents a connection to an RPC server.
type ProtoClient struct {
	idgen    func() ID // for subscriptions
	services *serviceRegistry

	idCounter atomic.Uint32

	// writeConn is used for writing to the connection on the caller's goroutine. It should
	// only be accessed outside of dispatch, with the write lock held. The write lock is
	// taken by sending on reqInit and released by sending on reqSent.
	writeConn *protobufCodec

	// for dispatch
	close       chan struct{}
	closing     chan struct{}         // closed when client is quitting
	didClose    chan struct{}         // closed when client quits
	reconnected chan ServerCodec      // where write/reconnect sends the new connection
	readOp      chan *GetStorageAtReq // read messages
	readErr     chan error            // errors from read
	reqInit     chan *requestOp       // register response IDs, takes write lock
	reqSent     chan error            // signals write completion, releases write lock
	reqTimeout  chan *requestOp       // removes response IDs when call timeout expires
}

func initProtoClient(conn *protobufCodec, idgen func() ID, services *serviceRegistry) *ProtoClient {
	c := &ProtoClient{
		idgen:       idgen,
		services:    services,
		writeConn:   conn,
		close:       make(chan struct{}),
		closing:     make(chan struct{}),
		didClose:    make(chan struct{}),
		reconnected: make(chan ServerCodec),
		readOp:      make(chan *GetStorageAtReq),
		readErr:     make(chan error),
		reqInit:     make(chan *requestOp),
		reqSent:     make(chan error, 1),
		reqTimeout:  make(chan *requestOp),
	}
	go c.dispatchProto(conn)
	return c
}

// Close closes the client, aborting any in-flight requests.
func (c *ProtoClient) Close() {
	select {
	case c.close <- struct{}{}:
		<-c.didClose
	case <-c.didClose:
	}
}

// copied from handleMessage in Handler, we just remove the sub-process tracking and timeouts and such
func (c *ProtoClient) handleMsg(msg *GetStorageAtReq, clientConn *protoClientConn) {
	// GetStorageAt()
	callb := c.services.callback("eth_getStorageAtHash")
	// log.Warn("callback is", "callback", callb)

	// Should be a BlockChainAPI
	if callb != nil {
		// log.Warn("callb recv is: %T", "type", callb.rcvr)
		// log.Warn("callb fn is: %T", "type", callb.fn)

		block_num := msg.Block()
		address_bytes, _ := msg.Address()
		slot_bytes, _ := msg.Slot()

		block := BlockNumberOrHashWithNumber(BlockNumber(block_num))
		address := common.BytesToAddress(address_bytes)
		slot := common.BytesToHash(slot_bytes)

		result, err := callb.call(context.Background(), "eth_getStorageAtHash", []reflect.Value{reflect.ValueOf(address), reflect.ValueOf(slot), reflect.ValueOf(block)})
		if err != nil {
			log.Error("error calling callback", "err", err)
			return
		}

		// log.Warn("result is: ", "result", result)
		value, ok := result.(*common.Hash)
		if !ok {
			log.Error("result is not a common.Hash", "result", result)
			return
		}

		clientConn.codec.writeStorageValue(context.Background(), value)
	}

	// clientConn.handler.handleCall()
	// answer := c.handleCallMsg(msg)
	// if answer != nil {
	// 	clientConn.codec.writeJSON(context.Background(), answer, false)
	// }
}

// handleCallMsg executes a call message and returns the answer.
// func (c *ProtoClient) handleCallMsg(msg *jsonrpcMessage) *jsonrpcMessage {
// 	start := time.Now()
// 	switch {
// 	case msg.isCall():
// 		resp := h.handleCall(ctx, msg)
// 		var ctx []interface{}
// 		ctx = append(ctx, "reqid", idForLog{msg.ID}, "duration", time.Since(start))
// 		if resp.Error != nil {
// 			ctx = append(ctx, "err", resp.Error.Message)
// 			if resp.Error.Data != nil {
// 				ctx = append(ctx, "errdata", resp.Error.Data)
// 			}
// 			h.log.Warn("Served "+msg.Method, ctx...)
// 		} else {
// 			h.log.Debug("Served "+msg.Method, ctx...)
// 		}
// 		return resp
// 	case msg.hasValidID():
// 		return msg.errorResponse(&invalidRequestError{"invalid request"})
// 	default:
// 		return errorMessage(&invalidRequestError{"invalid request"})
// 	}
// }

type protoClientConn struct {
	codec   *protobufCodec
	handler *handler
}

func (c *ProtoClient) newClientConn(conn *protobufCodec) *protoClientConn {
	ctx := context.Background()
	ctx = context.WithValue(ctx, clientContextKey{}, c)
	ctx = context.WithValue(ctx, peerInfoContextKey{}, conn.peerInfo())
	handler := newHandler(ctx, conn, c.idgen, c.services)
	return &protoClientConn{conn, handler}
}

// Copied from dispatch(), removes a few things that are not needed for protobuf (batch), and inlines the handle request logic so we don't have to spawn()
func (c *ProtoClient) dispatchProto(codec *protobufCodec) {
	var (
		lastOp      *requestOp  // tracks last send operation
		reqInitLock = c.reqInit // nil while the send lock is held
		conn        = c.newClientConn(codec)
		reading     = true
	)
	defer func() {
		close(c.closing)
		if reading {
			codec.close()
			c.drainRead()
		}
		close(c.didClose)
	}()

	// Spawn the initial read loop.
	go c.read(codec)

	for {
		select {
		case <-c.close:
			return

		// Read path:
		case msg := <-c.readOp:
			// log.Warn("msg is", "msg", msg)
			if msg != nil {
				c.handleMsg(msg, conn)
			}

		case err := <-c.readErr:
			conn.handler.log.Debug("RPC connection read error", "err", err)
			codec.close()
			reading = false

		// Send path:
		case op := <-reqInitLock:
			// Stop listening for further requests until the current one has been sent.
			reqInitLock = nil
			lastOp = op
			conn.handler.addRequestOp(op)

		case err := <-c.reqSent:
			if err != nil {
				// Remove response handlers for the last send. When the read loop
				// goes down, it will signal all other current operations.
				conn.handler.removeRequestOp(lastOp)
			}
			// Let the next request in.
			reqInitLock = c.reqInit
			lastOp = nil

		case op := <-c.reqTimeout:
			conn.handler.removeRequestOp(op)
		}
	}
}

// drainRead drops read messages until an error occurs.
func (c *ProtoClient) drainRead() {
	for {
		select {
		case <-c.readOp:
		case <-c.readErr:
			return
		}
	}
}

// read decodes RPC messages from a codec, feeding them into dispatch.
func (c *ProtoClient) read(codec *protobufCodec) {
	for {
		msg, err := codec.decode()
		if err != nil {
			log.Error("error from codec.decode()", "err", err)
			c.readErr <- err
			return
		}

		c.readOp <- msg
	}
}
