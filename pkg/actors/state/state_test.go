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

package state

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contribmd "github.com/dapr/components-contrib/metadata"
	contribstate "github.com/dapr/components-contrib/state"
	placementfake "github.com/dapr/dapr/pkg/actors/internal/placement/fake"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

type recordingStore struct {
	features []contribstate.Feature

	// multiMaxSize is the value the optional TransactionalStoreMultiMaxSize
	// method should return. 0 means the interface is not implemented.
	multiMaxSize int

	// deleteWithPrefixErr, when non-nil, is returned from DeleteWithPrefix.
	deleteWithPrefixErr error

	// deleteWithPrefixCount is returned from DeleteWithPrefix.Count.
	deleteWithPrefixCount int64

	// observedPrefix captures the prefix passed to DeleteWithPrefix on the
	// most recent call. Empty if never called.
	observedPrefix string

	// deleteWithPrefixCalls counts DeleteWithPrefix invocations.
	deleteWithPrefixCalls int
}

func (r *recordingStore) Init(context.Context, contribstate.Metadata) error { return nil }
func (r *recordingStore) Features() []contribstate.Feature                  { return r.features }
func (r *recordingStore) Close() error                                      { return nil }

func (r *recordingStore) Get(context.Context, *contribstate.GetRequest) (*contribstate.GetResponse, error) {
	return &contribstate.GetResponse{}, nil
}

func (r *recordingStore) Set(context.Context, *contribstate.SetRequest) error { return nil }
func (r *recordingStore) Delete(context.Context, *contribstate.DeleteRequest) error {
	return nil
}

func (r *recordingStore) BulkGet(context.Context, []contribstate.GetRequest, contribstate.BulkGetOpts) ([]contribstate.BulkGetResponse, error) {
	return nil, nil
}

func (r *recordingStore) BulkSet(context.Context, []contribstate.SetRequest, contribstate.BulkStoreOpts) error {
	return nil
}

func (r *recordingStore) BulkDelete(context.Context, []contribstate.DeleteRequest, contribstate.BulkStoreOpts) error {
	return nil
}

func (r *recordingStore) Multi(context.Context, *contribstate.TransactionalStateRequest) error {
	return nil
}

func (r *recordingStore) GetComponentMetadata() contribmd.MetadataMap { return contribmd.MetadataMap{} }

// DeleteWithPrefix records the call and returns the preconfigured result.
// Satisfies contribstate.DeleteWithPrefix.
func (r *recordingStore) DeleteWithPrefix(_ context.Context, req contribstate.DeleteWithPrefixRequest) (contribstate.DeleteWithPrefixResponse, error) {
	r.deleteWithPrefixCalls++
	r.observedPrefix = req.Prefix
	return contribstate.DeleteWithPrefixResponse{Count: r.deleteWithPrefixCount}, r.deleteWithPrefixErr
}

// withMultiMax wraps recordingStore and optionally implements
// TransactionalStoreMultiMaxSize. Kept separate so the base recordingStore
// stays a clean contribstate.Store — we attach the optional interface
// conditionally to mirror real contrib components.
type withMultiMax struct {
	*recordingStore
}

func (w withMultiMax) MultiMaxSize() int { return w.multiMaxSize }

// Assert interface satisfaction at compile time.
var (
	_ contribstate.Store                          = (*recordingStore)(nil)
	_ contribstate.TransactionalStore             = (*recordingStore)(nil)
	_ contribstate.DeleteWithPrefix               = (*recordingStore)(nil)
	_ contribstate.TransactionalStoreMultiMaxSize = withMultiMax{}
)

func newTestState(t *testing.T, store contribstate.Store) (*state, *recordingStore) {
	t.Helper()

	rec, ok := store.(*recordingStore)
	if !ok {
		if w, ok2 := store.(withMultiMax); ok2 {
			rec = w.recordingStore
		}
	}

	cs := compstore.New()
	cs.AddStateStore("test-store", store)
	require.NoError(t, cs.AddStateStoreActor("test-store", store))

	s := New(Options{
		AppID:      "test-app",
		StoreName:  "test-store",
		CompStore:  cs,
		Resiliency: resiliency.New(nil),
		Table:      tablefake.New(),
		Placement:  placementfake.New(),
	}).(*state)

	return s, rec
}

func TestDeleteActorState_UsesContribFastPathWhenFeaturePresent(t *testing.T) {
	store := &recordingStore{
		features: []contribstate.Feature{
			contribstate.FeatureETag,
			contribstate.FeatureTransactional,
			contribstate.FeatureDeleteWithPrefix,
		},
		deleteWithPrefixCount: 5,
	}
	s, rec := newTestState(t, store)

	ok, err := s.DeleteActorState(t.Context(), "wf", "inst-1")
	require.NoError(t, err)
	assert.True(t, ok, "DeleteActorState must return true when the store advertises FeatureDeleteWithPrefix")
	assert.Equal(t, 1, rec.deleteWithPrefixCalls, "contrib DeleteWithPrefix must be called exactly once")

	// The composite prefix is appID||actorType||actorID||, built the same
	// way as in the transactional path so state keys line up.
	assert.Equal(t, "test-app||wf||inst-1||", rec.observedPrefix,
		"DeleteActorState must pass the actor-key prefix with trailing DaprSeparator")
}

func TestDeleteActorState_ReturnsFalseWhenFeatureMissing(t *testing.T) {
	// No FeatureDeleteWithPrefix in Features(). The caller must fall back
	// to the transactional-delete path (see purgeWorkflowForce).
	store := &recordingStore{
		features: []contribstate.Feature{
			contribstate.FeatureETag,
			contribstate.FeatureTransactional,
		},
	}
	s, rec := newTestState(t, store)

	ok, err := s.DeleteActorState(t.Context(), "wf", "inst-2")
	require.NoError(t, err)
	assert.False(t, ok, "DeleteActorState must return (false, nil) when the feature is not advertised")
	assert.Zero(t, rec.deleteWithPrefixCalls, "contrib DeleteWithPrefix must not be called when the feature is absent")
}

func TestDeleteActorState_PropagatesContribError(t *testing.T) {
	boom := errors.New("boom")
	store := &recordingStore{
		features: []contribstate.Feature{
			contribstate.FeatureETag,
			contribstate.FeatureTransactional,
			contribstate.FeatureDeleteWithPrefix,
		},
		deleteWithPrefixErr: boom,
	}
	s, _ := newTestState(t, store)

	ok, err := s.DeleteActorState(t.Context(), "wf", "inst-3")
	assert.True(t, ok, "ok must be true so the caller knows the fast path was taken (and failed)")
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}

func TestMultiMaxSize_ReportsStoreLimit(t *testing.T) {
	base := &recordingStore{
		features: []contribstate.Feature{
			contribstate.FeatureETag,
			contribstate.FeatureTransactional,
		},
		multiMaxSize: 100,
	}
	store := withMultiMax{recordingStore: base}
	s, _ := newTestState(t, store)

	assert.Equal(t, 100, s.MultiMaxSize(),
		"MultiMaxSize must surface the contrib store's TransactionalStoreMultiMaxSize limit "+
			"(used by GetChunkedSaveRequest to split large workflow saves)")
}

func TestMultiMaxSize_ReturnsZeroWhenUnlimited(t *testing.T) {
	// Store does not implement TransactionalStoreMultiMaxSize → unlimited.
	store := &recordingStore{
		features: []contribstate.Feature{
			contribstate.FeatureETag,
			contribstate.FeatureTransactional,
		},
	}
	s, _ := newTestState(t, store)

	assert.Zero(t, s.MultiMaxSize(),
		"MultiMaxSize must return 0 when the store does not expose a limit")
}
