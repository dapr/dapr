/*
Copyright 2023 The Dapr Authors
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

package app

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
	testpb "github.com/dapr/dapr/tests/integration/framework/process/grpc/app/proto"
)

// Option is a function that configures the process.
type Option func(*options)

// App is a wrapper around a grpc.Server that implements a Dapr App.
type App struct {
	grpc *procgrpc.GRPC
	opts options

	streamCtx    context.Context
	streamCancel context.CancelFunc
	streamWG     sync.WaitGroup

	runOnce     sync.Once
	cleanupOnce sync.Once
}

func New(t *testing.T, fopts ...Option) *App {
	t.Helper()

	var opts options
	for _, fopt := range fopts {
		fopt(&opts)
	}

	return &App{
		opts: opts,
		grpc: procgrpc.New(t, append(opts.grpcopts, procgrpc.WithRegister(func(s *grpc.Server) {
			srv := &server{
				onInvokeFn:         opts.onInvokeFn,
				onJobEventFn:       opts.onJobEventFn,
				onTopicEventFn:     opts.onTopicEventFn,
				onBulkTopicEventFn: opts.onBulkTopicEventFn,
				listTopicSubFn:     opts.listTopicSubFn,
				listInputBindFn:    opts.listInputBindFn,
				onBindingEventFn:   opts.onBindingEventFn,
				healthCheckFn:      opts.healthCheckFn,
				pingFn:             opts.pingFn,
			}
			rtv1.RegisterAppCallbackServer(s, srv)
			rtv1.RegisterAppCallbackAlphaServer(s, srv)
			rtv1.RegisterAppCallbackHealthCheckServer(s, srv)
			testpb.RegisterTestServiceServer(s, srv)
			if opts.withRegister != nil {
				opts.withRegister(s)
			}
		}))...),
	}
}

func (a *App) Run(t *testing.T, ctx context.Context) {
	a.runOnce.Do(func() {
		a.grpc.Run(t, ctx)

		// Spawn the actor callback stream dialer if the test wired a daprd
		// address. The stream runs in the background until Cleanup.
		if a.opts.daprdGRPCAddrFn != nil {
			a.streamCtx, a.streamCancel = context.WithCancel(context.Background())
			a.streamWG.Go(func() {
				a.runActorCallbackStream(t)
			})
		}
	})
}

func (a *App) Cleanup(t *testing.T) {
	a.cleanupOnce.Do(func() {
		if a.streamCancel != nil {
			a.streamCancel()
			a.streamWG.Wait()
		}
		a.grpc.Cleanup(t)
	})
}

func (a *App) Port(t *testing.T) int {
	return a.grpc.Port(t)
}

// runActorCallbackStream dials daprd and runs a dispatch loop over the
// SubscribeActorEventsAlpha1 stream. Reconnects on transient errors until
// the harness context is cancelled.
func (a *App) runActorCallbackStream(t *testing.T) {
	for {
		if a.streamCtx.Err() != nil {
			return
		}
		addr := a.opts.daprdGRPCAddrFn()
		if addr == "" {
			select {
			case <-a.streamCtx.Done():
				return
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Logf("procgrpcapp: failed to dial daprd at %s: %v", addr, err)
			select {
			case <-a.streamCtx.Done():
				return
			case <-time.After(200 * time.Millisecond):
				continue
			}
		}

		a.pumpActorCallbackStream(t, conn)
		_ = conn.Close()

		// Brief pause before reconnecting so we don't spin if daprd keeps
		// rejecting the stream (e.g. not yet ready).
		select {
		case <-a.streamCtx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (a *App) pumpActorCallbackStream(t *testing.T, conn *grpc.ClientConn) {
	client := rtv1.NewDaprClient(conn)
	stream, err := client.SubscribeActorEventsAlpha1(a.streamCtx)
	if err != nil {
		return
	}

	// gRPC streams forbid concurrent Send calls from multiple goroutines, so
	// serialize every outbound write through this closure. Dispatcher
	// goroutines call send() instead of stream.Send directly.
	var sendMu sync.Mutex
	send := func(msg *rtv1.SubscribeActorEventsRequestAlpha1) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(msg)
	}

	initial := &rtv1.SubscribeActorEventsRequestInitialAlpha1{}
	if a.opts.actorRegistrationFn != nil {
		if v := a.opts.actorRegistrationFn(); v != nil {
			initial = v
		}
	}
	if sendErr := send(&rtv1.SubscribeActorEventsRequestAlpha1{
		RequestType: &rtv1.SubscribeActorEventsRequestAlpha1_InitialRequest{
			InitialRequest: initial,
		},
	}); sendErr != nil {
		return
	}

	// First response is the registration ack.
	resp, err := stream.Recv()
	if err != nil {
		return
	}
	if resp.GetInitialResponse() == nil {
		t.Logf("procgrpcapp: expected initial response, got %T", resp.GetResponseType())
		return
	}

	// Dispatch loop.
	for {
		msg, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) || a.streamCtx.Err() != nil {
				return
			}
			return
		}
		go a.dispatchActorCallback(send, msg)
	}
}

// dispatchActorCallback routes a single server → app callback message to
// the user-supplied handler and sends the response back through the
// serialized send closure. Runs on its own goroutine so the dispatch loop
// can keep reading.
func (a *App) dispatchActorCallback(
	send func(*rtv1.SubscribeActorEventsRequestAlpha1) error,
	msg *rtv1.SubscribeActorEventsResponseAlpha1,
) {
	ctx := a.streamCtx
	switch v := msg.GetResponseType().(type) {
	case *rtv1.SubscribeActorEventsResponseAlpha1_InvokeRequest:
		req := v.InvokeRequest
		resp := &rtv1.SubscribeActorEventsRequestInvokeResponseAlpha1{Id: req.GetId()}
		if a.opts.onActorInvokeFn != nil {
			r, err := a.opts.onActorInvokeFn(ctx, req)
			if err != nil {
				_ = send(requestFailed(req.GetId(), err))
				return
			}
			if r != nil {
				resp = r
				resp.Id = req.GetId()
			}
		}
		_ = send(&rtv1.SubscribeActorEventsRequestAlpha1{
			RequestType: &rtv1.SubscribeActorEventsRequestAlpha1_InvokeResponse{
				InvokeResponse: resp,
			},
		})
	case *rtv1.SubscribeActorEventsResponseAlpha1_ReminderRequest:
		req := v.ReminderRequest
		resp := &rtv1.SubscribeActorEventsRequestReminderResponseAlpha1{Id: req.GetId()}
		if a.opts.onActorReminderFn != nil {
			r, err := a.opts.onActorReminderFn(ctx, req)
			if err != nil {
				_ = send(requestFailed(req.GetId(), err))
				return
			}
			if r != nil {
				resp = r
				resp.Id = req.GetId()
			}
		}
		_ = send(&rtv1.SubscribeActorEventsRequestAlpha1{
			RequestType: &rtv1.SubscribeActorEventsRequestAlpha1_ReminderResponse{
				ReminderResponse: resp,
			},
		})
	case *rtv1.SubscribeActorEventsResponseAlpha1_TimerRequest:
		req := v.TimerRequest
		resp := &rtv1.SubscribeActorEventsRequestReminderResponseAlpha1{Id: req.GetId()}
		if a.opts.onActorTimerFn != nil {
			r, err := a.opts.onActorTimerFn(ctx, req)
			if err != nil {
				_ = send(requestFailed(req.GetId(), err))
				return
			}
			if r != nil {
				resp = r
				resp.Id = req.GetId()
			}
		}
		_ = send(&rtv1.SubscribeActorEventsRequestAlpha1{
			RequestType: &rtv1.SubscribeActorEventsRequestAlpha1_TimerResponse{
				TimerResponse: resp,
			},
		})
	case *rtv1.SubscribeActorEventsResponseAlpha1_DeactivateRequest:
		req := v.DeactivateRequest
		resp := &rtv1.SubscribeActorEventsRequestDeactivateResponseAlpha1{Id: req.GetId()}
		if a.opts.onActorDeactivateFn != nil {
			r, err := a.opts.onActorDeactivateFn(ctx, req)
			if err != nil {
				_ = send(requestFailed(req.GetId(), err))
				return
			}
			if r != nil {
				resp = r
				resp.Id = req.GetId()
			}
		}
		_ = send(&rtv1.SubscribeActorEventsRequestAlpha1{
			RequestType: &rtv1.SubscribeActorEventsRequestAlpha1_DeactivateResponse{
				DeactivateResponse: resp,
			},
		})
	}
}

func requestFailed(id string, err error) *rtv1.SubscribeActorEventsRequestAlpha1 {
	// Extract the gRPC status code so daprd can map it correctly —
	// notably codes.NotFound must surface as a permanent (non-retryable)
	// failure in the streaming transport
	// (pkg/actors/targets/app/transport/grpc/grpc.go). Handlers that
	// return a plain error without a status fall back to codes.Unknown.
	code := uint32(status.Code(err))
	return &rtv1.SubscribeActorEventsRequestAlpha1{
		RequestType: &rtv1.SubscribeActorEventsRequestAlpha1_RequestFailed{
			RequestFailed: &rtv1.SubscribeActorEventsRequestFailedAlpha1{
				Id:      id,
				Code:    code,
				Message: err.Error(),
			},
		},
	}
}
