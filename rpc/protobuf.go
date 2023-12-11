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
	"encoding/json"
	"errors"
	"sync"
	"time"

	"capnproto.org/go/capnp/v3"
	"github.com/ethereum/go-ethereum/log"
)

// NewCodec creates a codec on the given connection. If conn implements ConnRemoteAddr, log
// messages will use it to include the remote address of the connection.
func NewProtoCodec(conn Conn) ServerCodec {
	enc := json.NewEncoder(conn)
	// dec := json.NewDecoder(conn)
	// dec.UseNumber()

	encode := func(v interface{}, isErrorResponse bool) error {
		return enc.Encode(v)
	}

	codec := &protobufCodec{
		closeCh: make(chan interface{}),
		encode:  encode,
		decode: func(v interface{}) error {

			// Read bytes from conn
			b := make([]byte, 1024)
			n, err := conn.Read(b)
			if err != nil {
				log.Error("Failed Read", "err", err)
				return errors.New("failed Read")
			}
			log.Warn("n", "numbytes", n, "bytes", b)

			msg, err := capnp.UnmarshalPacked(b)
			if err != nil {
				log.Error("Failed Unmarshal", "err", err)
				return errors.New("failed Unmarshal")
			}

			getStorageReq, err := ReadRootGetStorageAtReq(msg)
			if err != nil {
				log.Error("Failed ReadRoot!", "err", err)
				return errors.New("failed ReadRoot")
			}

			block := getStorageReq.Block()
			addr, _ := getStorageReq.Address()
			slot, _ := getStorageReq.Slot()
			log.Warn("omg workd?", "block", block, "address", addr, "slot", slot)

			return errors.New("unimplemented, todo()")
			// return dec.Decode(v)
		},
		conn: conn,
	}
	if ra, ok := conn.(ConnRemoteAddr); ok {
		codec.remote = ra.RemoteAddr()
	}
	return codec
}

// jsonCodec reads and writes JSON-RPC messages to the underlying connection. It also has
// support for parsing arguments and serializing (result) objects.
type protobufCodec struct {
	remote  string
	closer  sync.Once        // close closed channel once
	closeCh chan interface{} // closed on Close
	decode  decodeFunc       // decoder to allow multiple transports
	encMu   sync.Mutex       // guards the encoder
	encode  encodeFunc       // encoder to allow multiple transports
	conn    deadlineCloser
}

func (c *protobufCodec) close() {
	c.closer.Do(func() {
		close(c.closeCh)
		c.conn.Close()
	})
}

// Closed returns a channel which will be closed when Close is called
func (c *protobufCodec) closed() <-chan interface{} {
	return c.closeCh
}

func (c *protobufCodec) peerInfo() PeerInfo {
	// This returns "ipc" because all other built-in transports have a separate codec type.
	return PeerInfo{Transport: "ipc", RemoteAddr: c.remote}
}

// remoteAddr implements ServerCodec.
func (c *protobufCodec) remoteAddr() string {
	return c.remote
}

func (c *protobufCodec) readBatch() (messages []*jsonrpcMessage, batch bool, err error) {
	// Decode the next JSON object in the input stream.
	// This verifies basic syntax, etc.
	var rawmsg json.RawMessage
	if err := c.decode(&rawmsg); err != nil {
		return nil, false, err
	}
	messages, batch = parseMessage(rawmsg)
	for i, msg := range messages {
		if msg == nil {
			// Message is JSON 'null'. Replace with zero value so it
			// will be treated like any other invalid message.
			messages[i] = new(jsonrpcMessage)
		}
	}
	return messages, batch, nil
}

func (c *protobufCodec) writeJSON(ctx context.Context, v interface{}, isErrorResponse bool) error {
	c.encMu.Lock()
	defer c.encMu.Unlock()

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(defaultWriteTimeout)
	}
	c.conn.SetWriteDeadline(deadline)

	switch typ := v.(type) {
	case *jsonrpcMessage:
		log.Warn("type is a jsonrpcMessage")
		return c.encode(typ, isErrorResponse)
	case []*jsonrpcMessage:
		log.Warn("type is a slice of jsonrpcMessage")
		return c.encode(typ, isErrorResponse)
	default:
		log.Warn("I don't know what type this is? %T", v)
		return c.encode(v, isErrorResponse)
	}

}
