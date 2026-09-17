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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	placementfake "github.com/dapr/dapr/pkg/actors/internal/placement/fake"
	"github.com/dapr/dapr/pkg/actors/router"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	"github.com/dapr/dapr/pkg/actors/targets"
	targetsfake "github.com/dapr/dapr/pkg/actors/targets/fake"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
)

func TestCallReminderNonLocalTimerDropped(t *testing.T) {
	var lookups int
	plc := placementfake.New().WithLookupActor(func(ctx context.Context, req *api.LookupActorRequest) (*api.LookupActorResponse, context.Context, context.CancelCauseFunc, error) {
		lookups++
		return &api.LookupActorResponse{Local: false, Address: "remote:1234", AppID: "other"}, ctx, func(error) {}, nil
	})

	r := router.New(router.Options{
		Placement:  plc,
		Table:      tablefake.New(),
		Resiliency: resiliency.New(logger.NewLogger("test")),
	})

	// The nil GRPC manager panics if the remote-forward path is reached.
	err := r.CallReminder(t.Context(), &api.Reminder{
		ActorType: "abc",
		ActorID:   "foo",
		Name:      "tick",
		IsTimer:   true,
	})
	require.ErrorIs(t, err, actorerrors.ErrTimerFireNotLocal)
	assert.Equal(t, 1, lookups)
}

func TestCallReminderExecuteLocallySkipsPlacement(t *testing.T) {
	plc := placementfake.New().WithLookupActor(func(context.Context, *api.LookupActorRequest) (*api.LookupActorResponse, context.Context, context.CancelCauseFunc, error) {
		t.Fatal("a pull-dispatched reminder must not consult placement")
		return nil, nil, nil, nil
	})

	var invoked *api.Reminder
	target := targetsfake.New("dapr.internal.default.app.activity").(*targetsfake.Fake).WithInvokeReminder(func(_ context.Context, rem *api.Reminder) error {
		invoked = rem
		return nil
	})
	var created []string
	tbl := tablefake.New().WithGetOrCreate(func(actorType, actorID string) (targets.Interface, error) {
		created = append(created, actorType+"/"+actorID)
		return target, nil
	})

	r := router.New(router.Options{
		Placement:  plc,
		Table:      tbl,
		Resiliency: resiliency.New(logger.NewLogger("test")),
	})

	rem := &api.Reminder{
		ActorType:      "dapr.internal.default.app.activity",
		ActorID:        "wf::1::0",
		Name:           "run-activity",
		SkipLock:       true,
		ExecuteLocally: true,
	}
	require.NoError(t, r.CallReminder(t.Context(), rem))
	assert.Equal(t, []string{"dapr.internal.default.app.activity/wf::1::0"}, created)
	assert.Same(t, rem, invoked)
}

func TestCallReminderNonLocalRemoteReminderErrors(t *testing.T) {
	plc := placementfake.New().WithLookupActor(func(ctx context.Context, req *api.LookupActorRequest) (*api.LookupActorResponse, context.Context, context.CancelCauseFunc, error) {
		return &api.LookupActorResponse{Local: false, Address: "remote:1234", AppID: "other"}, ctx, func(error) {}, nil
	})

	r := router.New(router.Options{
		Placement:  plc,
		Table:      tablefake.New(),
		Resiliency: resiliency.New(logger.NewLogger("test")),
	})

	err := r.CallReminder(t.Context(), &api.Reminder{
		ActorType: "abc",
		ActorID:   "foo",
		Name:      "remind",
		IsRemote:  true,
	})
	require.ErrorContains(t, err, "remote actor moved")
}
