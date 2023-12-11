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
	"errors"
	"sync"
	"time"

	"capnproto.org/go/capnp/v3"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
)

// NewCodec creates a codec on the given connection. If conn implements ConnRemoteAddr, log
// messages will use it to include the remote address of the connection.
func NewProtoCodec(conn Conn) *protobufCodec {
	// enc := json.NewEncoder(conn)
	// dec := json.NewDecoder(conn)
	// dec.UseNumber()

	codec := &protobufCodec{
		closeCh: make(chan interface{}),
		conn:    conn,
		buffer:  make([]byte, 1024),
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
	encMu   sync.Mutex       // guards the encoder
	encode  encodeFunc       // encoder to allow multiple transports
	conn    Conn
	buffer  []byte
}

func (c *protobufCodec) decode() (*GetStorageAtReq, error) {
	// Read bytes from conn
	_, err := c.conn.Read(c.buffer)
	if err != nil {
		log.Error("Failed Read", "err", err)
		return nil, errors.New("failed Read")
	}
	// log.Warn("n", "numbytes", n)

	msg, err := capnp.Unmarshal(c.buffer)
	if err != nil {
		log.Error("Failed Unmarshal", "err", err)
		return nil, errors.New("failed Unmarshal")
	}

	getStorageReq, err := ReadRootGetStorageAtReq(msg)
	if err != nil {
		log.Error("Failed ReadRoot!", "err", err)
		return nil, errors.New("failed ReadRoot")
	}

	// block := getStorageReq.Block()
	// addr, _ := getStorageReq.Address()
	// slot, _ := getStorageReq.Slot()
	// log.Warn("omg workd?", "block", block, "address", addr, "slot", slot)

	// return errors.New("unimplemented, todo()")
	// return dec.Decode(v)
	return &getStorageReq, nil
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

func (c *protobufCodec) writeStorageValue(ctx context.Context, value *common.Hash) error {
	c.encMu.Lock()
	defer c.encMu.Unlock()

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(defaultWriteTimeout)
	}
	c.conn.SetWriteDeadline(deadline)

	arena := capnp.SingleSegment(nil)
	msg, seg, err := capnp.NewMessage(arena)
	if err != nil {
		log.Error("Failed NewMessage", "err", err)
		return errors.New("failed NewMessage")
	}

	response, err := NewRootGetStorageAtReq(seg)
	if err != nil {
		log.Error("Failed NewRootGetStorageAtReq", "err", err)
		return errors.New("failed NewRootGetStorageAtReq")
	}

	response.SetSlot(value[:])
	bytes, err := msg.Marshal()
	if err != nil {
		log.Error("Failed Marshal", "err", err)
		return errors.New("failed Marshal")
	}

	// log.Warn("writing to conn", "bytes", bytes)
	_, err = c.conn.Write(bytes)
	// log.Warn("wrote to conn", "numbytes", n)

	return err
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
