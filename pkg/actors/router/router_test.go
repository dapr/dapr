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

package router_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/dapr/pkg/actors/api"
	placementfake "github.com/dapr/dapr/pkg/actors/internal/placement/fake"
	"github.com/dapr/dapr/pkg/actors/router"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	"github.com/dapr/dapr/pkg/modes"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	securityfake "github.com/dapr/dapr/pkg/security/fake"
	"github.com/dapr/kit/logger"
)

// A fired reminder forwarded to the actor's owner carries the creator the
// Scheduler verified, so the owner can judge it as if delivered directly.
func TestCallReminderRemoteForwardsSourceAppID(t *testing.T) {
	received := make(chan *internalv1pb.Reminder, 1)
	srv := grpc.NewServer()
	internalv1pb.RegisterServiceInvocationServer(srv, &reminderSink{received: received})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)

	plc := placementfake.New().WithLookupActor(func(ctx context.Context, req *api.LookupActorRequest) (*api.LookupActorResponse, context.Context, context.CancelCauseFunc, error) {
		return &api.LookupActorResponse{Local: false, Address: lis.Addr().String(), AppID: "target"}, ctx, func(error) {}, nil
	})

	r := router.New(router.Options{
		Namespace:  "default",
		Placement:  plc,
		Table:      tablefake.New(),
		Resiliency: resiliency.New(logger.NewLogger("test")),
		GRPC:       manager.NewManager(securityfake.New(), modes.StandaloneMode, &manager.AppChannelConfig{}),
	})

	recv := func(t *testing.T) *internalv1pb.Reminder {
		t.Helper()
		select {
		case got := <-received:
			return got
		case <-time.After(time.Second * 5):
			require.Fail(t, "the owner did not receive the forwarded reminder")
			return nil
		}
	}

	require.NoError(t, r.CallReminder(t.Context(), &api.Reminder{
		ActorType:   "dapr.internal.default.target.workflow",
		ActorID:     "instance-1",
		Name:        "activity-result-abc",
		SourceAppID: "creator",
	}))
	got := recv(t)
	require.NotNil(t, got.SourceAppId)
	assert.Equal(t, "creator", got.GetSourceAppId())
	assert.Equal(t, "activity-result-abc", got.GetName())
	assert.Equal(t, "instance-1", got.GetActorId())

	require.NoError(t, r.CallReminder(t.Context(), &api.Reminder{
		ActorType: "dapr.internal.default.target.workflow",
		ActorID:   "instance-1",
		Name:      "activity-result-unknown",
	}))
	assert.Nil(t, recv(t).SourceAppId, "an unknown creator is unset, not empty")
}

type reminderSink struct {
	internalv1pb.UnimplementedServiceInvocationServer
	received chan *internalv1pb.Reminder
}

func (s *reminderSink) CallActorReminder(_ context.Context, in *internalv1pb.Reminder) (*emptypb.Empty, error) {
	s.received <- in
	return new(emptypb.Empty), nil
}
