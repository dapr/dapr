/*
Copyright 2026 The Dapr Authors
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

package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/pkg/actors/callbackstream"
	"github.com/dapr/dapr/pkg/config"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
)

func newStreamTransport(t *testing.T) (*Transport, *callbackstream.Manager) {
	t.Helper()

	mgr := callbackstream.NewManager()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = mgr.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	return New(mgr, resiliency.New(logger.NewLogger("test")), "myactortype"), mgr
}

func serveDeactivate(t *testing.T, conn *callbackstream.Connection, reply func(id string) *runtimev1pb.SubscribeActorEventsRequestAlpha1) <-chan *runtimev1pb.SubscribeActorEventsResponseDeactivateRequestAlpha1 {
	t.Helper()

	seen := make(chan *runtimev1pb.SubscribeActorEventsResponseDeactivateRequestAlpha1, 8)
	go func() {
		for {
			select {
			case <-conn.Done():
				return
			case out := <-conn.Outbox:
				req := out.GetDeactivateRequest()
				seen <- req
				conn.Deliver(reply(req.GetId()))
			}
		}
	}()

	return seen
}

func TestEvict_SendsDeactivateRequest(t *testing.T) {
	tp, mgr := newStreamTransport(t)
	conn := mgr.Register(t.Context(), &config.ApplicationConfig{Entities: []string{"myactortype"}})
	t.Cleanup(func() { mgr.Close(conn, nil) })

	seen := serveDeactivate(t, conn, func(id string) *runtimev1pb.SubscribeActorEventsRequestAlpha1 {
		return &runtimev1pb.SubscribeActorEventsRequestAlpha1{
			RequestType: &runtimev1pb.SubscribeActorEventsRequestAlpha1_DeactivateResponse{
				DeactivateResponse: &runtimev1pb.SubscribeActorEventsRequestDeactivateResponseAlpha1{Id: id},
			},
		}
	})

	require.NoError(t, tp.Evict(t.Context(), "myactortype", "a"))
	req := <-seen
	require.NotNil(t, req)
	assert.Equal(t, "myactortype", req.GetActorType())
	assert.Equal(t, "a", req.GetActorId())
}

func TestEvict_RequestFailedIsNotAnError(t *testing.T) {
	tp, mgr := newStreamTransport(t)
	conn := mgr.Register(t.Context(), &config.ApplicationConfig{Entities: []string{"myactortype"}})
	t.Cleanup(func() { mgr.Close(conn, nil) })

	seen := serveDeactivate(t, conn, func(id string) *runtimev1pb.SubscribeActorEventsRequestAlpha1 {
		return &runtimev1pb.SubscribeActorEventsRequestAlpha1{
			RequestType: &runtimev1pb.SubscribeActorEventsRequestAlpha1_RequestFailed{
				RequestFailed: &runtimev1pb.SubscribeActorEventsRequestFailedAlpha1{
					Id:      id,
					Code:    uint32(codes.NotFound),
					Message: "actor id not found",
				},
			},
		}
	})

	require.NoError(t, tp.Evict(t.Context(), "myactortype", "a"))
	assert.Equal(t, "a", (<-seen).GetActorId())
}

func TestEvict_NoConnectionReturned(t *testing.T) {
	tp, _ := newStreamTransport(t)
	require.ErrorIs(t, tp.Evict(t.Context(), "myactortype", "a"), callbackstream.ErrNoConnection)
}
