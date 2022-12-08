/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package wasm

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"

	"dubbo.apache.org/dubbo-go/v3/common/logger"
	"github.com/taction/daprwasmactor/sdk"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/assemblyscript"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	msgpack "github.com/wapc/tinygo-msgpack"

	"github.com/dapr/dapr/pkg/actors"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
)

const (
	i32               = api.ValueTypeI32
	functionActorCall = "__actor_call"
)

type Module struct {
	instanceCounter uint64
	runtime         wazero.Runtime
	compiled        wazero.CompiledModule
}

// WazeroRuntime implements NewRuntime by returning a wazero runtime with WASI
// and AssemblyScript host functions instantiated.
func WazeroRuntime(ctx context.Context) (wazero.Runtime, error) {
	r := wazero.NewRuntime(ctx)

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		_ = r.Close(ctx)
		return nil, err
	}

	// This disables the abort message as no other engines write it.
	envBuilder := r.NewHostModuleBuilder("env")
	assemblyscript.NewFunctionExporter().WithAbortMessageDisabled().ExportFunctions(envBuilder)
	if _, err := envBuilder.Instantiate(ctx, r); err != nil {
		_ = r.Close(ctx)
		return nil, err
	}
	return r, nil
}

func NewModule(ctx context.Context, guest []byte, a actors.Actors) (m *Module, err error) {
	r, err := WazeroRuntime(ctx)
	if err != nil {
		return nil, err
	}

	m = &Module{runtime: r}

	if _, err = instantiateWasmHost(ctx, r, a); err != nil {
		_ = r.Close(ctx)
		return
	}

	if m.compiled, err = r.CompileModule(ctx, guest); err != nil {
		_ = r.Close(ctx)
		return
	}
	return
}

// instantiateWasmHost instantiates a wasmHost and returns it and its corresponding module, or an error.
func instantiateWasmHost(ctx context.Context, r wazero.Runtime, a actors.Actors) (api.Module, error) {
	w := &wasmHost{a: a}
	return r.NewHostModuleBuilder("dapr_actor").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(w.GetActorState), []api.ValueType{i32, i32, i32}, []api.ValueType{i32}).
		WithParameterNames("ptr", "size", "res_ptr").
		Export("get_actor_state").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(w.CallActor), []api.ValueType{i32, i32, i32}, []api.ValueType{i32}).
		WithParameterNames("ptr", "size", "res_ptr").
		Export("call_actor").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(w.SetActorState), []api.ValueType{i32, i32}, []api.ValueType{i32}).
		WithParameterNames("ptr", "size").
		Export("set_actor_state").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(w.ActorRequest), []api.ValueType{i32}, []api.ValueType{}).
		WithParameterNames("ptr").
		Export("actor_request").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(w.ActorResponse), []api.ValueType{i32, i32}, []api.ValueType{}).
		WithParameterNames("ptr", "size").
		Export("actor_response").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(w.ActorErr), []api.ValueType{i32, i32}, []api.ValueType{}).
		WithParameterNames("ptr", "size").
		Export("actor_error").
		Instantiate(ctx, r)
}

type wasmHost struct {
	a actors.Actors
}

func (w *wasmHost) GetActorState(ctx context.Context, m api.Module, stack []uint64) (res []uint64) {
	ptr := uint32(stack[0])
	size := uint32(stack[1])
	resPtr := uint32(stack[2])
	reqByte, success := m.Memory().Read(ctx, ptr, size)
	if !success {
		panic("failed to read memory") // todo: return error
	}
	de := msgpack.NewDecoder(reqByte)
	req, err := sdk.DecodeGetActorStateRequest(&de)
	if err != nil {
		panic(err) // todo: return error
	}
	resp, err := w.a.GetState(ctx, &actors.GetStateRequest{ActorID: req.ActorID, ActorType: req.ActorType, Key: req.KeyName})
	if err != nil {
		panic(err) // todo: return error
	}
	sdkres := sdk.GetActorStateResponse{Data: resp.Data}
	resBytes, _ := sdkres.Encode()
	resSize := uint32(len(resBytes))
	m.Memory().Write(ctx, resPtr, resBytes)
	return []uint64{uint64(resSize)}
}

func (w *wasmHost) CallActor(ctx context.Context, m api.Module, stack []uint64) []uint64 {
	ptr := uint32(stack[0])
	size := uint32(stack[0])
	resPtr := uint32(stack[2])
	reqByte, success := m.Memory().Read(ctx, ptr, size)
	if !success {
		panic("failed to read memory") // todo: return error
	}
	de := msgpack.NewDecoder(reqByte)
	req, err := sdk.DecodeInvokeActorRequest(&de)
	if err != nil {
		panic(err) // todo: return error
	}
	invokeReq := invokev1.NewInvokeMethodRequest(req.Method)
	invokeReq.WithRawData(req.Data, req.ContentType)
	invokeReq.WithActor(req.ActorType, req.ActorID)
	resp, err := w.a.Call(ctx, invokeReq)
	if err != nil {
		panic(err) // todo: return error
	}
	ct, data := resp.RawData()
	res := &sdk.InvokeActorResponse{Data: data, ContentType: ct, StatusCode: resp.Status().Code}
	resByte, err := res.Encode()
	if err != nil {
		panic(err) // todo: return error
	}
	resSize := uint32(len(resByte))
	m.Memory().Write(ctx, resPtr, resByte)
	return []uint64{uint64(resSize)}
}

func (w *wasmHost) SetActorState(ctx context.Context, m api.Module, stack []uint64) (res []uint64) {
	ptr := uint32(stack[0])
	size := uint32(stack[1])
	reqByte, success := m.Memory().Read(ctx, ptr, size)
	if !success {
		logger.Errorf("failed to read memory")
		return []uint64{uint64(0)}
	}
	de := msgpack.NewDecoder(reqByte)
	req, err := sdk.DecodeTransactionalRequest(&de)
	if err != nil {
		logger.Error(err)
		return []uint64{uint64(0)}
	}
	actorReq := &actors.TransactionalRequest{ActorID: req.ActorID, ActorType: req.ActorType}
	for _, op := range req.Operations {
		switch op.Operation {
		case sdk.Upsert:
			actorReq.Operations = append(actorReq.Operations, actors.TransactionalOperation{Operation: actors.Upsert, Request: actors.TransactionalUpsert{Key: op.Request.Key, Value: op.Request.Value}})
		case sdk.Delete:
			actorReq.Operations = append(actorReq.Operations, actors.TransactionalOperation{Operation: actors.Delete, Request: actors.TransactionalDelete{Key: op.Request.Key}})
		}
	}
	err = w.a.TransactionalStateOperation(ctx, actorReq)
	if err != nil {
		logger.Error(err)
		return []uint64{uint64(0)}
	}
	return []uint64{uint64(1)}
}

func (w *wasmHost) ActorRequest(ctx context.Context, m api.Module, stack []uint64) (res []uint64) {
	ptr := uint32(stack[0])
	info := ctx.Value(ActorCallCtxKey{}).(*ActorCallInfo)
	m.Memory().Write(ctx, ptr, info.req)
	return
}

func (w *wasmHost) ActorResponse(ctx context.Context, m api.Module, stack []uint64) (res []uint64) {
	var success bool
	ptr := uint32(stack[0])
	size := uint32(stack[1])
	info := ctx.Value(ActorCallCtxKey{}).(*ActorCallInfo)
	info.res, success = m.Memory().Read(ctx, ptr, size)
	if !success {
		info.err = "failed to read response memory"
	}
	return
}

func (w *wasmHost) ActorErr(ctx context.Context, m api.Module, stack []uint64) (res []uint64) {
	ptr := uint32(stack[0])
	size := uint32(stack[1])
	info := ctx.Value(ActorCallCtxKey{}).(*ActorCallInfo)
	err, success := m.Memory().Read(ctx, ptr, size)
	if !success {
		info.err = "failed to read error memory"
	} else {
		info.err = string(err)
	}
	return
}

type GetActorStateRequest struct {
	ActorType string
	ActorID   string
	KeyName   string
}

type GetActorStateResponse struct {
	Data []byte
}

type InvokeActorRequest struct {
	ActorType   string
	ActorID     string
	Method      string
	Data        []byte
	ContentType string
}

type InvokeActorResponse struct {
	Data        []byte
	ContentType string
	StatusCode  int32
}

type Instance struct {
	module api.Module
	Call   api.Function
}

type ActorCallCtxKey struct{}

type ActorCallInfo struct {
	req []byte
	res []byte
	err string
}

func (i *Instance) ActorCalled(ctx context.Context, req *sdk.InvokeActorRequest) (data *sdk.InvokeActorResponse) {
	data = &sdk.InvokeActorResponse{StatusCode: 500}
	reqByte, err := req.Encode()
	if err != nil {
		return &sdk.InvokeActorResponse{
			Data:       []byte(err.Error()),
			StatusCode: 500,
		}
	}
	info := ActorCallInfo{req: reqByte}
	ctx = context.WithValue(ctx, ActorCallCtxKey{}, &info)
	callres, err := i.Call.Call(ctx, uint64(len(reqByte)))
	if err != nil {
		data.Data = []byte(err.Error())
		return
	}
	if callres[0] == 1 {
		d := msgpack.NewDecoder(info.res)
		res, err := sdk.DecodeInvokeActorResponse(&d)
		if err != nil {
			return &sdk.InvokeActorResponse{
				Data:       []byte(err.Error()),
				StatusCode: 500,
			}
		}
		return &res
	}
	return
}

func (i *Instance) Close(ctx context.Context) error {
	return i.module.Close(ctx)
}

func (m *Module) Instantiate(ctx context.Context) (WasmInstance, error) {
	config := wazero.NewModuleConfig().
		WithStartFunctions("_start", "start"). // tinygo compile main as _start.
		WithStdout(os.Stdout).
		WithStderr(os.Stderr)
	moduleName := fmt.Sprintf("%d", atomic.AddUint64(&m.instanceCounter, 1))

	module, err := m.runtime.InstantiateModule(ctx, m.compiled, config.WithName(moduleName))
	if err != nil {
		return nil, err
	}

	instance := &Instance{module: module}

	if instance.Call = module.ExportedFunction(functionActorCall); instance.Call == nil {
		_ = module.Close(ctx)
		return nil, fmt.Errorf("module %s didn't export function %s", moduleName, functionActorCall)
	}

	return instance, nil
}

func (m *Module) Check() error {
	// todo check exported functions exist
	return nil
}
