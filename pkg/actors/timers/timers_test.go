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

package timers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/api"
	placementfake "github.com/dapr/dapr/pkg/actors/internal/placement/fake"
	"github.com/dapr/dapr/pkg/actors/internal/timers/inmemory"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
)

func newTestTimers(t *testing.T, local bool, lookupErr error) Interface {
	t.Helper()
	storage := inmemory.New(inmemory.Options{Router: routerfake.New()})
	t.Cleanup(func() { require.NoError(t, storage.Close()) })

	plc := placementfake.New().WithLookupActor(func(ctx context.Context, req *api.LookupActorRequest) (*api.LookupActorResponse, context.Context, context.CancelCauseFunc, error) {
		if lookupErr != nil {
			return nil, ctx, func(error) {}, lookupErr
		}
		return &api.LookupActorResponse{Local: local}, ctx, func(error) {}, nil
	})

	return New(Options{
		Storage:   storage,
		Table:     tablefake.New().WithIsActorTypeHosted(func(string) bool { return true }),
		Placement: plc,
	})
}

func TestCreateOwnedActor(t *testing.T) {
	ts := newTestTimers(t, true, nil)
	require.NoError(t, ts.Create(t.Context(), &api.CreateTimerRequest{
		ActorType: "abc", ActorID: "foo", Name: "tick", DueTime: "1h",
	}))
}

func TestCreateNotOwnedActor(t *testing.T) {
	ts := newTestTimers(t, false, nil)
	err := ts.Create(t.Context(), &api.CreateTimerRequest{
		ActorType: "abc", ActorID: "foo", Name: "tick", DueTime: "1h",
	})
	require.ErrorIs(t, err, ErrTimerActorNotOwned)
}

func TestCreateLookupError(t *testing.T) {
	lerr := errors.New("placement unavailable")
	ts := newTestTimers(t, true, lerr)
	err := ts.Create(t.Context(), &api.CreateTimerRequest{
		ActorType: "abc", ActorID: "foo", Name: "tick", DueTime: "1h",
	})
	require.ErrorIs(t, err, lerr)
}

func TestCreateTypeNotHosted(t *testing.T) {
	storage := inmemory.New(inmemory.Options{Router: routerfake.New()})
	t.Cleanup(func() { require.NoError(t, storage.Close()) })
	ts := New(Options{
		Storage:   storage,
		Table:     tablefake.New().WithIsActorTypeHosted(func(string) bool { return false }),
		Placement: placementfake.New(),
	})
	err := ts.Create(t.Context(), &api.CreateTimerRequest{
		ActorType: "abc", ActorID: "foo", Name: "tick", DueTime: "1h",
	})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrTimerActorNotOwned)
}

func TestDeleteOwnedActor(t *testing.T) {
	ts := newTestTimers(t, true, nil)
	require.NoError(t, ts.Delete(t.Context(), &api.DeleteTimerRequest{
		ActorType: "abc", ActorID: "foo", Name: "tick",
	}))
}

func TestDeleteNotOwnedActor(t *testing.T) {
	ts := newTestTimers(t, false, nil)
	err := ts.Delete(t.Context(), &api.DeleteTimerRequest{
		ActorType: "abc", ActorID: "foo", Name: "tick",
	})
	require.ErrorIs(t, err, ErrTimerActorNotOwned)
}

func TestListOwnedActor(t *testing.T) {
	ts := newTestTimers(t, true, nil)
	ctx := t.Context()

	got, err := ts.List(ctx, &api.ListTimersRequest{ActorType: "abc", ActorID: "foo"})
	require.NoError(t, err)
	assert.Empty(t, got)

	require.NoError(t, ts.Create(ctx, &api.CreateTimerRequest{
		ActorType: "abc", ActorID: "foo", Name: "tick", DueTime: "1h", Period: "10s", Callback: "cb",
	}))
	require.NoError(t, ts.Create(ctx, &api.CreateTimerRequest{
		ActorType: "abc", ActorID: "bar", Name: "other", DueTime: "1h",
	}))

	got, err = ts.List(ctx, &api.ListTimersRequest{ActorType: "abc", ActorID: "foo"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "tick", got[0].Name)
	assert.Equal(t, "abc", got[0].ActorType)
	assert.Equal(t, "foo", got[0].ActorID)
	assert.Equal(t, "1h", got[0].DueTime)
	assert.Equal(t, "10s", got[0].Period.String())
	assert.Equal(t, "cb", got[0].Callback)
	assert.True(t, got[0].IsTimer)
}

func TestListNotOwnedActor(t *testing.T) {
	ts := newTestTimers(t, false, nil)
	got, err := ts.List(t.Context(), &api.ListTimersRequest{ActorType: "abc", ActorID: "foo"})
	require.ErrorIs(t, err, ErrTimerActorNotOwned)
	assert.Nil(t, got)
}

func TestListLookupError(t *testing.T) {
	lerr := errors.New("placement unavailable")
	ts := newTestTimers(t, true, lerr)
	_, err := ts.List(t.Context(), &api.ListTimersRequest{ActorType: "abc", ActorID: "foo"})
	require.ErrorIs(t, err, lerr)
}

func TestListTypeNotHosted(t *testing.T) {
	storage := inmemory.New(inmemory.Options{Router: routerfake.New()})
	t.Cleanup(func() { require.NoError(t, storage.Close()) })
	ts := New(Options{
		Storage:   storage,
		Table:     tablefake.New().WithIsActorTypeHosted(func(string) bool { return false }),
		Placement: placementfake.New(),
	})
	_, err := ts.List(t.Context(), &api.ListTimersRequest{ActorType: "abc", ActorID: "foo"})
	require.ErrorIs(t, err, ErrTimerActorTypeNotHosted)
	assert.NotErrorIs(t, err, ErrTimerActorNotOwned)
}

func TestGetOwnedActor(t *testing.T) {
	ts := newTestTimers(t, true, nil)
	ctx := t.Context()

	got, err := ts.Get(ctx, &api.GetTimerRequest{ActorType: "abc", ActorID: "foo", Name: "tick"})
	require.NoError(t, err)
	assert.Nil(t, got)

	require.NoError(t, ts.Create(ctx, &api.CreateTimerRequest{
		ActorType: "abc", ActorID: "foo", Name: "tick", DueTime: "1h", Period: "10s", Callback: "cb",
	}))

	got, err = ts.Get(ctx, &api.GetTimerRequest{ActorType: "abc", ActorID: "foo", Name: "tick"})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "tick", got.Name)
	assert.Equal(t, "abc", got.ActorType)
	assert.Equal(t, "foo", got.ActorID)
	assert.Equal(t, "1h", got.DueTime)
	assert.Equal(t, "10s", got.Period.String())
	assert.Equal(t, "cb", got.Callback)
	assert.True(t, got.IsTimer)

	got, err = ts.Get(ctx, &api.GetTimerRequest{ActorType: "abc", ActorID: "foo", Name: "other"})
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestGetNotOwnedActor(t *testing.T) {
	ts := newTestTimers(t, false, nil)
	got, err := ts.Get(t.Context(), &api.GetTimerRequest{ActorType: "abc", ActorID: "foo", Name: "tick"})
	require.ErrorIs(t, err, ErrTimerActorNotOwned)
	assert.Nil(t, got)
}

func TestGetLookupError(t *testing.T) {
	lerr := errors.New("placement unavailable")
	ts := newTestTimers(t, true, lerr)
	_, err := ts.Get(t.Context(), &api.GetTimerRequest{ActorType: "abc", ActorID: "foo", Name: "tick"})
	require.ErrorIs(t, err, lerr)
}

func TestGetTypeNotHosted(t *testing.T) {
	storage := inmemory.New(inmemory.Options{Router: routerfake.New()})
	t.Cleanup(func() { require.NoError(t, storage.Close()) })
	ts := New(Options{
		Storage:   storage,
		Table:     tablefake.New().WithIsActorTypeHosted(func(string) bool { return false }),
		Placement: placementfake.New(),
	})
	_, err := ts.Get(t.Context(), &api.GetTimerRequest{ActorType: "abc", ActorID: "foo", Name: "tick"})
	require.ErrorIs(t, err, ErrTimerActorTypeNotHosted)
}
