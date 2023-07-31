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

type LogTracerResult struct {
	Logs []SingleLog `json:"logs"`
}

type SingleLog struct {
	Address common.Address `json:"address"`
	Topics  []common.Hash  `json:"topics"`
	Data    []byte         `json:"data"`
}

type logTracer struct {
	env       *vm.EVM
	result    LogTracerResult
	interrupt uint32 // Atomic flag to signal execution interruption
	reason    error  // Textual reason for the interruption
}

// newlogTracer returns a native go tracer which tracks
// call frames of a tx, and implements vm.EVMLogger.
func newlogTracer(ctx *tracers.Context, cfg json.RawMessage) (tracers.Tracer, error) {
	// First callframe contains tx context info
	// and is populated on start and end.
	return &logTracer{
		result: LogTracerResult{
			Logs: make([]SingleLog, 0),
		},
	}, nil
}

// CaptureStart implements the EVMLogger interface to initialize the tracing operation.
func (t *logTracer) CaptureStart(env *vm.EVM, from common.Address, to common.Address, create bool, input []byte, gas uint64, value *big.Int) {
	t.env = env
}

// CaptureEnd is called after the call finishes to finalize the tracing.
func (t *logTracer) CaptureEnd(output []byte, gasUsed uint64, _ time.Duration, err error) {
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
		// LOGX opcode stack is: offset, size, topics[X]...
		offset := stackData[stackLen-1]
		size := stackData[stackLen-2]
		data := scope.Memory.GetCopy(int64(offset.Uint64()), int64(size.Uint64()))

		topics := make([]common.Hash, numParams)
		for i := 0; i < numParams; i++ {
			topics[i] = common.Hash(stackData[stackLen-3-i].Bytes32())
		}

		t.result.Logs = append(t.result.Logs, SingleLog{
			Address: scope.Contract.Address(),
			Data:    data,
			Topics:  topics,
		})
	}
}

// CaptureFault implements the EVMLogger interface to trace an execution fault.
func (t *logTracer) CaptureFault(pc uint64, op vm.OpCode, gas, cost uint64, _ *vm.ScopeContext, depth int, err error) {
}

// CaptureEnter is called when EVM enters a new scope (via call, create or selfdestruct).
func (t *logTracer) CaptureEnter(typ vm.OpCode, from common.Address, to common.Address, input []byte, gas uint64, value *big.Int) {
}

// CaptureExit is called when EVM exits a scope, even if the scope didn't
// execute any code.
func (t *logTracer) CaptureExit(output []byte, gasUsed uint64, err error) {
}

func (*logTracer) CaptureTxStart(gasLimit uint64) {}

func (*logTracer) CaptureTxEnd(restGas uint64) {}

// GetResult returns the json-encoded nested list of call traces, and any
// error arising from the encoding or forceful termination (via `Stop`).
func (t *logTracer) GetResult() (json.RawMessage, error) {
	res, err := json.Marshal(t.result)
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
