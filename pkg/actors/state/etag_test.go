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
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/cenkalti/backoff/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	contribstate "github.com/dapr/components-contrib/state"
	inmemory "github.com/dapr/components-contrib/state/in-memory"
	"github.com/dapr/dapr/pkg/actors/api"
	resiliencyV1alpha1 "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

type recordingStore struct {
	Backend
	features []contribstate.Feature

	lock       sync.Mutex
	ops        [][]contribstate.TransactionalStateOperation
	errs       []error
	withETags  int
	dropETags  bool
	noGetETags bool
}

// Advertising the feature isn't enough, MultiWithETags makes it strict.
type strictStore struct {
	*recordingStore
}

func (r *recordingStore) Features() []contribstate.Feature {
	if r.features != nil {
		return r.features
	}
	return r.Backend.Features()
}

func (r *recordingStore) record(req *contribstate.TransactionalStateRequest, withETags bool) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.ops = append(r.ops, slices.Clone(req.Operations))
	if withETags {
		r.withETags++
	}
	if len(r.errs) > 0 {
		err := r.errs[0]
		r.errs = r.errs[1:]
		return err
	}
	return nil
}

func (r *recordingStore) Multi(ctx context.Context, req *contribstate.TransactionalStateRequest) error {
	if err := r.record(req, false); err != nil {
		return err
	}
	return r.Backend.Multi(ctx, req)
}

func (r *recordingStore) Get(ctx context.Context, req *contribstate.GetRequest) (*contribstate.GetResponse, error) {
	resp, err := r.Backend.Get(ctx, req)
	if resp != nil && r.noGetETags {
		resp.ETag = nil
	}
	return resp, err
}

func (s strictStore) MultiWithETags(ctx context.Context, req *contribstate.TransactionalStateRequest) (*contribstate.TransactionalStateResponse, error) {
	if err := s.record(req, true); err != nil {
		return nil, err
	}
	resp, err := s.Backend.(contribstate.TransactionalStoreWithETags).MultiWithETags(ctx, req)
	if resp != nil && s.dropETags {
		resp.ETags = nil
	}
	return resp, err
}

func (r *recordingStore) attempts() int {
	r.lock.Lock()
	defer r.lock.Unlock()
	return len(r.ops)
}

func (r *recordingStore) last() []contribstate.TransactionalStateOperation {
	r.lock.Lock()
	defer r.lock.Unlock()
	return r.ops[len(r.ops)-1]
}

func newETagState(t *testing.T, features ...contribstate.Feature) (Interface, *recordingStore) {
	t.Helper()
	return newETagStateWithResiliency(t, resiliency.New(logger.NewLogger(t.Name())), features...)
}

func newETagStateWithResiliency(t *testing.T, res resiliency.Provider, features ...contribstate.Feature) (Interface, *recordingStore) {
	t.Helper()
	store := &recordingStore{
		Backend:  inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name())).(Backend),
		features: features,
	}
	return newStoreState(t, res, strictStore{store}), store
}

func newNonStrictState(t *testing.T, features ...contribstate.Feature) (Interface, *recordingStore) {
	t.Helper()
	store := &recordingStore{
		Backend:  inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name())).(Backend),
		features: features,
	}
	return newStoreState(t, resiliency.New(logger.NewLogger(t.Name())), store), store
}

func newStoreState(t *testing.T, res resiliency.Provider, store Backend) Interface {
	t.Helper()
	cs := compstore.New()
	require.NoError(t, cs.AddStateStoreActor("mystore", store))
	return New(Options{
		AppID:      "myapp",
		CompStore:  cs,
		Resiliency: res,
	})
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stdout) })
	return &buf
}

func captureDebugLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := captureLog(t)
	log.SetOutputLevel(logger.DebugLevel)
	t.Cleanup(func() { log.SetOutputLevel(logger.InfoLevel) })
	return buf
}

func upsert(key, value string, etag *string) api.TransactionalOperation {
	return api.TransactionalOperation{
		Operation: api.Upsert,
		Request:   api.TransactionalUpsert{Key: key, Value: []byte(value), ETag: etag},
	}
}

func create(key, value string) api.TransactionalOperation {
	return api.TransactionalOperation{
		Operation: api.Upsert,
		Request:   api.TransactionalUpsert{Key: key, Value: []byte(value), CreateOnly: true},
	}
}

func del(key string, etag *string) api.TransactionalOperation {
	return api.TransactionalOperation{
		Operation: api.Delete,
		Request:   api.TransactionalDelete{Key: key, ETag: etag},
	}
}

func txn(firstWrite bool, ops ...api.TransactionalOperation) *api.TransactionalRequest {
	return &api.TransactionalRequest{
		ActorType:      "mytype",
		ActorID:        "myid",
		Operations:     ops,
		FirstWriteETag: firstWrite,
	}
}

func get(t *testing.T, s Interface, key string) *api.StateResponse {
	t.Helper()
	resp, err := s.Get(t.Context(), &api.GetStateRequest{ActorType: "mytype", ActorID: "myid", Key: key}, false)
	require.NoError(t, err)
	return resp
}

func getStrict(t *testing.T, s Interface, key string) *api.StateResponse {
	t.Helper()
	resp, err := s.Get(t.Context(), &api.GetStateRequest{ActorType: "mytype", ActorID: "myid", Key: key, StrictETag: true}, false)
	require.NoError(t, err)
	return resp
}

func TestTransactionalStateOperationETag(t *testing.T) {
	t.Run("get returns the etag that a write then needs", func(t *testing.T) {
		s, _ := newETagState(t)
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v1", nil)), false))

		first := get(t, s, "k")
		require.NotNil(t, first.ETag)
		assert.Equal(t, "v1", string(first.Data))

		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v2", first.ETag)), false))
		second := get(t, s, "k")
		require.NotNil(t, second.ETag)
		assert.NotEqual(t, *first.ETag, *second.ETag)
		assert.Equal(t, "v2", string(second.Data))
	})

	t.Run("stale etag fails the whole transaction", func(t *testing.T) {
		s, _ := newETagState(t)
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v1", nil), upsert("other", "o1", nil)), false))
		stale := get(t, s, "k").ETag
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v2", nil)), false))

		err := s.TransactionalStateOperation(t.Context(), true, txn(true,
			upsert("other", "o2", nil),
			upsert("k", "v3", stale),
		), false)
		require.ErrorIs(t, err, messages.ErrActorStateETagMismatch)
		st, ok := grpcstatus.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Aborted, st.Code())
		var apiErr messages.APIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.HTTPCode())
		assert.Equal(t, "ERR_ACTOR_STATE_ETAG_MISMATCH", apiErr.Tag())

		assert.Equal(t, "v2", string(get(t, s, "k").Data))
		assert.Equal(t, "o1", string(get(t, s, "other").Data))
	})

	t.Run("stale etag on delete keeps the value", func(t *testing.T) {
		s, _ := newETagState(t)
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v1", nil)), false))
		stale := get(t, s, "k").ETag
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v2", nil)), false))

		err := s.TransactionalStateOperation(t.Context(), true, txn(true, del("k", stale)), false)
		require.ErrorIs(t, err, messages.ErrActorStateETagMismatch)
		assert.Equal(t, "v2", string(get(t, s, "k").Data))

		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, del("k", get(t, s, "k").ETag)), false))
		assert.Empty(t, get(t, s, "k").Data)
	})

	t.Run("etag on a missing key is a mismatch", func(t *testing.T) {
		s, _ := newETagState(t)
		err := s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v1", ptr.Of("1"))), false)
		require.ErrorIs(t, err, messages.ErrActorStateETagMismatch)
		assert.Empty(t, get(t, s, "k").Data)
	})

	t.Run("no etag is last write wins", func(t *testing.T) {
		for _, etag := range []*string{nil, ptr.Of("")} {
			s, store := newETagState(t)
			require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v1", etag)), false))
			require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v2", etag), del("gone", etag)), false))
			assert.Equal(t, "v2", string(get(t, s, "k").Data))

			ops := store.last()
			require.Len(t, ops, 2)
			assert.Empty(t, ops[0].(contribstate.SetRequest).Options.Concurrency)
			assert.Empty(t, ops[1].(contribstate.DeleteRequest).Options.Concurrency)
		}
	})

	t.Run("etag ops are sent with first write concurrency", func(t *testing.T) {
		s, store := newETagState(t)
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v1", nil), upsert("d", "x", nil)), false))
		etagK := get(t, s, "k").ETag
		etagD := get(t, s, "d").ETag

		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true,
			upsert("k", "v2", etagK),
			del("d", etagD),
			upsert("plain", "p", nil),
		), false))

		ops := store.last()
		require.Len(t, ops, 3)
		set := ops[0].(contribstate.SetRequest)
		assert.Equal(t, contribstate.FirstWrite, set.Options.Concurrency)
		assert.Equal(t, etagK, set.ETag)
		assert.Equal(t, contribstate.FirstWrite, ops[1].(contribstate.DeleteRequest).Options.Concurrency)
		assert.Empty(t, ops[2].(contribstate.SetRequest).Options.Concurrency)
	})

	t.Run("internal callers keep their behavior", func(t *testing.T) {
		s, store := newETagState(t)
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(false, upsert("k", "v1", nil)), false))
		stale := get(t, s, "k").ETag
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(false, upsert("k", "v2", nil)), false))

		err := s.TransactionalStateOperation(t.Context(), true, txn(false, upsert("k", "v3", stale)), false)
		require.Error(t, err)
		var etagErr *contribstate.ETagError
		require.ErrorAs(t, err, &etagErr)
		assert.Equal(t, contribstate.ETagMismatch, etagErr.Kind())
		require.NotErrorIs(t, err, messages.ErrActorStateETagMismatch)
		require.NotErrorAs(t, err, new(messages.APIError))
		assert.Empty(t, store.last()[0].(contribstate.SetRequest).Options.Concurrency)
	})

	t.Run("store without etag support is rejected", func(t *testing.T) {
		s, _ := newETagState(t, contribstate.FeatureTransactional)
		err := s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v1", ptr.Of("1"))), false)
		require.EqualError(t, err, errStateStoreNotConfigured)
	})
}

func TestTransactionalStateOperationETagRetryPolicy(t *testing.T) {
	const maxRetries = 3

	newState := func(t *testing.T) (Interface, *recordingStore) {
		t.Helper()
		return newETagStateWithResiliency(t, resiliency.FromConfigurations(logger.NewLogger(t.Name()), &resiliencyV1alpha1.Resiliency{
			Spec: resiliencyV1alpha1.ResiliencySpec{
				Policies: resiliencyV1alpha1.Policies{
					Retries: map[string]resiliencyV1alpha1.Retry{
						"retry": {
							MaxRetries: ptr.Of(maxRetries),
							Policy:     "constant",
							Duration:   "1ms",
						},
					},
				},
				Targets: resiliencyV1alpha1.Targets{
					Components: map[string]resiliencyV1alpha1.ComponentPolicyNames{
						"mystore": {
							Outbound: resiliencyV1alpha1.PolicyNames{Retry: "retry"},
						},
					},
				},
			},
		}))
	}

	staleETag := func(t *testing.T, s Interface, firstWrite bool) *string {
		t.Helper()
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(firstWrite, upsert("k", "v1", nil)), false))
		stale := get(t, s, "k").ETag
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(firstWrite, upsert("k", "v2", nil)), false))
		return stale
	}

	t.Run("etag mismatch is attempted once", func(t *testing.T) {
		s, store := newState(t)
		stale := staleETag(t, s, true)
		before := store.attempts()

		err := s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v3", stale)), false)
		require.ErrorIs(t, err, messages.ErrActorStateETagMismatch)
		st, ok := grpcstatus.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Aborted, st.Code())
		assert.Equal(t, 1, store.attempts()-before)
		assert.Equal(t, "v2", string(get(t, s, "k").Data))
	})

	t.Run("invalid etag is attempted once", func(t *testing.T) {
		s, store := newState(t)
		store.errs = []error{contribstate.NewETagError(contribstate.ETagInvalid, errors.New("bad etag"))}

		err := s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v1", ptr.Of("x"))), false)
		require.ErrorIs(t, err, messages.ErrActorStateETagInvalid)
		assert.Equal(t, 1, store.attempts())
	})

	t.Run("non etag error is still retried", func(t *testing.T) {
		s, store := newState(t)
		store.errs = []error{errors.New("transient"), errors.New("transient")}

		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v1", nil)), false))
		assert.Equal(t, 3, store.attempts())
		assert.Equal(t, "v1", string(get(t, s, "k").Data))
	})

	t.Run("internal callers keep retrying etag errors", func(t *testing.T) {
		s, store := newState(t)
		stale := staleETag(t, s, false)
		before := store.attempts()

		err := s.TransactionalStateOperation(t.Context(), true, txn(false, upsert("k", "v3", stale)), false)
		var etagErr *contribstate.ETagError
		require.ErrorAs(t, err, &etagErr)
		assert.Equal(t, contribstate.ETagMismatch, etagErr.Kind())
		require.NotErrorAs(t, err, new(messages.APIError))
		require.NotErrorAs(t, err, new(*backoff.PermanentError))
		assert.Equal(t, maxRetries+1, store.attempts()-before)
	})
}
