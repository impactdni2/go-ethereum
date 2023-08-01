// Copyright 2021 The go-ethereum Authors
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

package native

import (
	"encoding/json"
	"math/big"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/eth/tracers"
)

func init() {
	register("logTracer", newlogTracer)
}

type LogCallFrame struct {
	Logs []SingleLog `json:"logs"`
}

type SingleLog struct {
	Address common.Address `json:"address"`
	Topics  []common.Hash  `json:"topics"`
	Data    []byte         `json:"data"`
}

type logTracer struct {
	env       *vm.EVM
	callstack []*LogCallFrame

	interrupt uint32 // Atomic flag to signal execution interruption
	reason    error  // Textual reason for the interruption
}

// newlogTracer returns a native go tracer which tracks
// call frames of a tx, and implements vm.EVMLogger.
func newlogTracer(ctx *tracers.Context, cfg json.RawMessage) (tracers.Tracer, error) {
	// First LogCallFrame contains tx context info
	// and is populated on start and end.
	return &logTracer{
		callstack: []*LogCallFrame{
			{
				Logs: make([]SingleLog, 0),
			},
		},
	}, nil
}

// CaptureStart implements the EVMLogger interface to initialize the tracing operation.
func (t *logTracer) CaptureStart(env *vm.EVM, from common.Address, to common.Address, create bool, input []byte, gas uint64, value *big.Int) {
	// Called for each transaction...
	// log.Printf("captureStart (Transaction Start)")
	// We handle them as a layer of the stack since we might need to revert if the whole txn fails
	t.CaptureEnter(0, common.Address{}, common.Address{}, nil, 0, nil)
	t.env = env
}

// CaptureEnd is called after the call finishes to finalize the tracing.
func (t *logTracer) CaptureEnd(output []byte, gasUsed uint64, _ time.Duration, err error) {
	// Called for each transaction
	// log.Printf("captureEnd (Transaction End), err: %s", err)
	t.CaptureExit(nil, 0, err)
}

// CaptureEnter is called when EVM enters a new scope (via call, create or selfdestruct).
func (t *logTracer) CaptureEnter(typ vm.OpCode, from common.Address, to common.Address, input []byte, gas uint64, value *big.Int) {
	// Push a new stack scope...
	t.callstack = append(t.callstack, &LogCallFrame{})
	// log.Printf("CaptureEnter, new stack pushed")
}

// CaptureState implements the EVMLogger interface to trace a single step of VM execution.
func (t *logTracer) CaptureState(pc uint64, op vm.OpCode, gas, cost uint64, scope *vm.ScopeContext, rData []byte, depth int, err error) {
	stack := scope.Stack
	stackData := stack.Data()
	stackLen := len(stackData)
	numParams := -1

	switch {
	case op == vm.LOG0:
		numParams = 0
	case op == vm.LOG1:
		numParams = 1
	case op == vm.LOG2:
		numParams = 2
	case op == vm.LOG3:
		numParams = 3
	case op == vm.LOG4:
		numParams = 4
	}

	if numParams > -1 {
		call := t.callstack[len(t.callstack)-1]
		// log.Printf("Found a LOG%d opcode, logs before: %d, pushing!", numParams, len(call.Logs))

		// LOGX opcode stack is: offset, size, topics[X]...
		offset := stackData[stackLen-1]
		size := stackData[stackLen-2]
		data := scope.Memory.GetCopy(int64(offset.Uint64()), int64(size.Uint64()))

		topics := make([]common.Hash, numParams)
		for i := 0; i < numParams; i++ {
			topics[i] = common.Hash(stackData[stackLen-3-i].Bytes32())
		}

		call.Logs = append(call.Logs, SingleLog{
			Address: scope.Contract.Address(),
			Data:    data,
			Topics:  topics,
		})
		// log.Printf("len after pushing: %d", len(call.Logs))
	}
}

// CaptureFault implements the EVMLogger interface to trace an execution fault.
func (t *logTracer) CaptureFault(pc uint64, op vm.OpCode, gas, cost uint64, _ *vm.ScopeContext, depth int, err error) {
}

// CaptureExit is called when EVM exits a scope, even if the scope didn't execute any code.
func (t *logTracer) CaptureExit(output []byte, gasUsed uint64, err error) {
	size := len(t.callstack)
	// log.Printf("captureExit, stacksize: %d", size)
	if size <= 1 {
		// log.Printf("SIZE <= 1, RETURNING")
		return
	}

	// pop call
	call := t.callstack[size-1]
	// log.Printf("top call is at %d, %s", size-1, call)
	t.callstack = t.callstack[:size-1]
	// log.Printf("stack len is now: %d", len(t.callstack))
	size -= 1
	// log.Printf("CaptureExit, numLogs %d, err? %t", len(call.Logs), err != nil)

	// If the scope errored, we just throw away the stack
	if err == nil && len(call.Logs) > 0 {
		// log.Printf("adding %d logs up one layer size-1: %d, length of callstack: %d", len(call.Logs), size-1, len(t.callstack))
		// log.Printf("callstack at that spot: %s", t.callstack[size-1])
		// log.Printf("logs at that: %s", t.callstack[size-1].Logs)
		// log.Printf("logs to add: %s", call.Logs)
		t.callstack[size-1].Logs = append(t.callstack[size-1].Logs, call.Logs...)
	}
}

func (*logTracer) CaptureTxStart(gasLimit uint64) {}

func (*logTracer) CaptureTxEnd(restGas uint64) {}

// GetResult returns the json-encoded nested list of call traces, and any
// error arising from the encoding or forceful termination (via `Stop`).
func (t *logTracer) GetResult() (json.RawMessage, error) {
	// log.Printf("getResult, size: %d", len(t.callstack))
	// log.Printf("result is: %s", t.callstack[0])
	// log.Printf("getresult log length: %d", len(t.callstack[0].Logs))
	res, err := json.Marshal(t.callstack[0].Logs)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(res), t.reason
}

// Stop terminates execution of the tracer at the first opportune moment.
func (t *logTracer) Stop(err error) {
	t.reason = err
	atomic.StoreUint32(&t.interrupt, 1)
}

func (*logTracer) CaptureArbitrumStorageGet(key common.Hash, depth int, before bool) {
}
func (*logTracer) CaptureArbitrumStorageSet(key common.Hash, value common.Hash, depth int, before bool) {
}
func (*logTracer) CaptureArbitrumTransfer(env *vm.EVM, from *common.Address, to *common.Address, value *big.Int, before bool, purpose string) {
}
