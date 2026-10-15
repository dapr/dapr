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
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	contribstate "github.com/dapr/components-contrib/state"
	inmemory "github.com/dapr/components-contrib/state/in-memory"
	"github.com/dapr/dapr/pkg/actors/api"
	resiliencyV1alpha1 "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	componentsstate "github.com/dapr/dapr/pkg/components/state"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

var (
	nonStrictFeatures = []contribstate.Feature{contribstate.FeatureETag, contribstate.FeatureTransactional}
	noCreateOnly      = []contribstate.Feature{contribstate.FeatureETag, contribstate.FeatureTransactional, contribstate.FeatureTransactionalETags}
	allFeatures       = []contribstate.Feature{contribstate.FeatureETag, contribstate.FeatureTransactional, contribstate.FeatureTransactionalETags, contribstate.FeatureCreateOnly}
)

func assertAPIError(t *testing.T, err error, want messages.APIError, code codes.Code, httpCode int) {
	t.Helper()
	require.ErrorIs(t, err, want)
	st, ok := grpcstatus.FromError(err)
	require.True(t, ok)
	assert.Equal(t, code, st.Code())
	var apiErr messages.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, httpCode, apiErr.HTTPCode())
}

func TestNonStrictStore(t *testing.T) {
	stores := map[string]func(t *testing.T) (Interface, *recordingStore){
		"no TRANSACTIONAL_ETAGS feature": func(t *testing.T) (Interface, *recordingStore) {
			return newETagState(t, nonStrictFeatures...)
		},
		"feature without MultiWithETags": func(t *testing.T) (Interface, *recordingStore) {
			return newNonStrictState(t, allFeatures...)
		},
	}

	for name, newState := range stores {
		t.Run(name, func(t *testing.T) {
			t.Run("public read has no etag, internal read keeps it", func(t *testing.T) {
				s, _ := newState(t)
				require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v1", nil)), false))

				resp := getStrict(t, s, "k")
				assert.Equal(t, "v1", string(resp.Data))
				assert.Nil(t, resp.ETag)

				assert.NotNil(t, get(t, s, "k").ETag)
			})

			t.Run("etag is rejected before the store and logged", func(t *testing.T) {
				s, store := newState(t)
				require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v1", nil)), false))
				etag := get(t, s, "k").ETag
				before := store.attempts()
				logs := captureLog(t)

				for _, op := range []func() error{
					func() error {
						return s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("other", "o", nil), upsert("k", "v2", etag)), false)
					},
					func() error {
						return s.TransactionalStateOperation(t.Context(), true, txn(true, del("k", etag)), false)
					},
					func() error {
						_, err := s.TransactionalStateOperationWithETags(t.Context(), true, txn(true, upsert("k", "v2", etag)), false)
						return err
					},
				} {
					err := op()
					assertAPIError(t, err, messages.ErrActorStateETagUnsupported, codes.FailedPrecondition, http.StatusBadRequest)
					assert.Contains(t, err.Error(), "mystore")
				}
				assert.Equal(t, before, store.attempts())
				assert.Equal(t, "v1", string(get(t, s, "k").Data))
				assert.Equal(t, 1, strings.Count(logs.String(), "Rejected an actor state transaction with an etag for actor type 'mytype': actor state store 'mystore'"))
			})

			t.Run("create-only is a plain upsert", func(t *testing.T) {
				s, store := newState(t)
				logs := captureDebugLog(t)
				require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, create("k", "v1")), false))
				etags, err := s.TransactionalStateOperationWithETags(t.Context(), true, txn(true, create("k", "v2")), false)
				require.NoError(t, err)
				assert.Empty(t, etags)
				assert.False(t, store.last()[0].(contribstate.SetRequest).Options.CreateOnly)
				assert.Equal(t, "v2", string(get(t, s, "k").Data))
				assert.Equal(t, 2, strings.Count(logs.String(), "Ignoring createOnly on actor state key"))
			})

			t.Run("create-only with an etag op is still rejected", func(t *testing.T) {
				s, store := newState(t)
				err := s.TransactionalStateOperation(t.Context(), true, txn(true, create("n", "x"), upsert("k", "v", ptr.Of("1"))), false)
				assertAPIError(t, err, messages.ErrActorStateETagUnsupported, codes.FailedPrecondition, http.StatusBadRequest)
				assert.Equal(t, 0, store.attempts())
				assert.Empty(t, get(t, s, "n").Data)
			})

			t.Run("no etag is last write wins and returns no etags", func(t *testing.T) {
				s, store := newState(t)
				require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v1", nil)), false))
				etags, err := s.TransactionalStateOperationWithETags(t.Context(), true, txn(true, upsert("k", "v2", nil), upsert("n", "x", nil)), false)
				require.NoError(t, err)
				assert.NotNil(t, etags)
				assert.Empty(t, etags)
				assert.Equal(t, "v2", string(get(t, s, "k").Data))
				assert.Equal(t, 0, store.withETags)
			})

			t.Run("internal callers keep their behavior", func(t *testing.T) {
				s, store := newState(t)
				require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(false, upsert("k", "v1", nil)), false))
				etag := get(t, s, "k").ETag
				require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(false, upsert("k", "v2", etag)), false))
				set := store.last()[0].(contribstate.SetRequest)
				assert.Equal(t, etag, set.ETag)
				assert.Empty(t, set.Options.Concurrency)
				assert.Equal(t, "v2", string(get(t, s, "k").Data))
			})
		})
	}
}

func TestStrictStoreReturnedETags(t *testing.T) {
	t.Run("returned etags equal a subsequent get", func(t *testing.T) {
		s, store := newETagState(t)
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("gone", "g", nil), upsert("back", "b", nil)), false))

		etags, err := s.TransactionalStateOperationWithETags(t.Context(), true, txn(true,
			upsert("a", "1", nil),
			upsert("b", "2", nil),
			del("gone", nil),
			upsert("tmp", "t", nil),
			del("tmp", nil),
			del("back", nil),
			upsert("back", "b2", nil),
		), false)
		require.NoError(t, err)
		assert.Equal(t, 1, store.withETags)

		require.Len(t, etags, 3)
		for _, k := range []string{"a", "b", "back"} {
			resp := getStrict(t, s, k)
			require.NotNil(t, resp.ETag)
			assert.Equal(t, *resp.ETag, etags[k], k)
		}
		assert.NotContains(t, etags, "gone")
		assert.NotContains(t, etags, "tmp")
	})

	t.Run("returned etag guards the next save without a re-read", func(t *testing.T) {
		s, _ := newETagState(t)
		etags, err := s.TransactionalStateOperationWithETags(t.Context(), true, txn(true, upsert("k", "v1", nil)), false)
		require.NoError(t, err)
		stale := etags["k"]
		require.NotEmpty(t, stale)

		etags, err = s.TransactionalStateOperationWithETags(t.Context(), true, txn(true, upsert("k", "v2", &stale)), false)
		require.NoError(t, err)
		fresh := etags["k"]
		require.NotEmpty(t, fresh)
		assert.NotEqual(t, stale, fresh)

		_, err = s.TransactionalStateOperationWithETags(t.Context(), true, txn(true, upsert("k", "stale", &stale)), false)
		assertAPIError(t, err, messages.ErrActorStateETagMismatch, codes.Aborted, http.StatusConflict)
		assert.Equal(t, "v2", string(get(t, s, "k").Data))

		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, del("k", &fresh)), false))
		assert.Empty(t, get(t, s, "k").Data)
	})

	t.Run("without the opt-in Multi is used and nothing is returned", func(t *testing.T) {
		s, store := newETagState(t)
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v1", nil)), false))
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(false, upsert("k", "v2", nil)), false))
		assert.Equal(t, 2, store.attempts())
		assert.Equal(t, 0, store.withETags)
	})

	t.Run("mismatch returns no etags", func(t *testing.T) {
		s, _ := newETagState(t)
		etags, err := s.TransactionalStateOperationWithETags(t.Context(), true, txn(true, upsert("k", "v1", ptr.Of("1"))), false)
		assertAPIError(t, err, messages.ErrActorStateETagMismatch, codes.Aborted, http.StatusConflict)
		assert.Nil(t, etags)
	})

	t.Run("missing returned etag is dropped and logged once per actor type", func(t *testing.T) {
		s, store := newETagState(t)
		store.dropETags = true
		logs := captureLog(t)

		for range 2 {
			etags, err := s.TransactionalStateOperationWithETags(t.Context(), true, txn(true, upsert("k", "v1", nil)), false)
			require.NoError(t, err)
			assert.Empty(t, etags)
		}
		assert.Equal(t, 1, strings.Count(logs.String(), "returned no etag on a transaction for actor type 'mytype'"))
	})
}

func TestStrictStoreRead(t *testing.T) {
	t.Run("public read returns the etag", func(t *testing.T) {
		s, _ := newETagState(t)
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v1", nil)), false))
		resp := getStrict(t, s, "k")
		require.NotNil(t, resp.ETag)
		assert.Equal(t, get(t, s, "k").ETag, resp.ETag)
	})

	t.Run("missing etag on an existing key is logged once per actor type", func(t *testing.T) {
		s, store := newETagState(t)
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v1", nil)), false))
		store.noGetETags = true
		logs := captureLog(t)

		for range 3 {
			resp := getStrict(t, s, "k")
			assert.Equal(t, "v1", string(resp.Data))
			assert.Nil(t, resp.ETag)
		}
		assert.Empty(t, getStrict(t, s, "absent").Data)
		get(t, s, "k")
		assert.Equal(t, 1, strings.Count(logs.String(), "returned no etag on a read for actor type 'mytype'"))
	})

	t.Run("absent key logs nothing", func(t *testing.T) {
		s, store := newETagState(t)
		store.noGetETags = true
		logs := captureLog(t)
		resp := getStrict(t, s, "absent")
		assert.Empty(t, resp.Data)
		assert.Nil(t, resp.ETag)
		assert.Empty(t, logs.String())
	})
}

func TestCreateOnly(t *testing.T) {
	t.Run("creates an absent key", func(t *testing.T) {
		s, store := newETagState(t)
		etags, err := s.TransactionalStateOperationWithETags(t.Context(), true, txn(true, create("k", "v1")), false)
		require.NoError(t, err)
		assert.Equal(t, *getStrict(t, s, "k").ETag, etags["k"])
		set := store.last()[0].(contribstate.SetRequest)
		assert.True(t, set.Options.CreateOnly)
		assert.Nil(t, set.ETag)
	})

	t.Run("existing key fails the whole transaction", func(t *testing.T) {
		s, _ := newETagState(t)
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "newer", nil)), false))

		err := s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("other", "o", nil), create("k", "stale")), false)
		assertAPIError(t, err, messages.ErrActorStateETagMismatch, codes.Aborted, http.StatusConflict)
		assert.Equal(t, "newer", string(get(t, s, "k").Data))
		assert.Empty(t, get(t, s, "other").Data)
	})

	t.Run("with an etag is invalid", func(t *testing.T) {
		s, store := newETagState(t)
		op := api.TransactionalOperation{
			Operation: api.Upsert,
			Request:   api.TransactionalUpsert{Key: "k", Value: []byte("v"), ETag: ptr.Of("1"), CreateOnly: true},
		}
		err := s.TransactionalStateOperation(t.Context(), true, txn(true, op), false)
		assertAPIError(t, err, messages.ErrActorStateETagInvalid, codes.InvalidArgument, http.StatusBadRequest)
		assert.Equal(t, 0, store.attempts())
	})

	t.Run("on a delete is invalid", func(t *testing.T) {
		s, store := newETagState(t)
		op := api.TransactionalOperation{
			Operation: api.Delete,
			Request:   map[string]any{"key": "k", "createOnly": true},
		}
		err := s.TransactionalStateOperation(t.Context(), true, txn(true, op), false)
		assertAPIError(t, err, messages.ErrBadRequest, codes.InvalidArgument, http.StatusBadRequest)
		assert.Equal(t, 0, store.attempts())
	})

	t.Run("decoded from a request map", func(t *testing.T) {
		s, store := newETagState(t)
		op := api.TransactionalOperation{
			Operation: api.Upsert,
			Request:   map[string]any{"key": "k", "value": "v", "createOnly": true},
		}
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, op), false))
		assert.True(t, store.last()[0].(contribstate.SetRequest).Options.CreateOnly)
	})

	t.Run("strict store without CREATE_ONLY upserts it and still takes etags", func(t *testing.T) {
		s, store := newETagState(t, noCreateOnly...)
		logs := captureDebugLog(t)
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, create("k", "v1")), false))
		etags, err := s.TransactionalStateOperationWithETags(t.Context(), true, txn(true, create("k", "v2")), false)
		require.NoError(t, err)
		assert.False(t, store.last()[0].(contribstate.SetRequest).Options.CreateOnly)
		assert.Equal(t, "v2", string(get(t, s, "k").Data))
		assert.Contains(t, logs.String(), "Ignoring createOnly on actor state key")
		assert.Contains(t, logs.String(), string(contribstate.FeatureCreateOnly))

		require.NotEmpty(t, etags["k"])
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v3", ptr.Of(etags["k"]))), false))
		err = s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "stale", ptr.Of(etags["k"]))), false)
		assertAPIError(t, err, messages.ErrActorStateETagMismatch, codes.Aborted, http.StatusConflict)
	})

	t.Run("internal callers never send it", func(t *testing.T) {
		s, store := newETagState(t)
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(false, upsert("k", "v1", nil)), false))
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(false, create("k", "v2")), false))
		assert.False(t, store.last()[0].(contribstate.SetRequest).Options.CreateOnly)
		assert.Equal(t, "v2", string(get(t, s, "k").Data))
	})

	t.Run("conflict is attempted once", func(t *testing.T) {
		s, store := newETagStateWithResiliency(t, resiliency.FromConfigurations(logger.NewLogger(t.Name()), &resiliencyV1alpha1.Resiliency{
			Spec: resiliencyV1alpha1.ResiliencySpec{
				Policies: resiliencyV1alpha1.Policies{
					Retries: map[string]resiliencyV1alpha1.Retry{
						"retry": {MaxRetries: ptr.Of(3), Policy: "constant", Duration: "1ms"},
					},
				},
				Targets: resiliencyV1alpha1.Targets{
					Components: map[string]resiliencyV1alpha1.ComponentPolicyNames{
						"mystore": {Outbound: resiliencyV1alpha1.PolicyNames{Retry: "retry"}},
					},
				},
			},
		}))
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v1", nil)), false))
		before := store.attempts()
		_, err := s.TransactionalStateOperationWithETags(t.Context(), true, txn(true, create("k", "v2")), false)
		assertAPIError(t, err, messages.ErrActorStateETagMismatch, codes.Aborted, http.StatusConflict)
		assert.Equal(t, 1, store.attempts()-before)
	})
}

func TestSupportOfPluggable(t *testing.T) {
	sup := supportOf(componentsstate.NewGRPCStateStore(logger.NewLogger(t.Name()), "mystore"))
	assert.False(t, sup.strict)
	assert.False(t, sup.createOnly)
	assert.Equal(t, "is a pluggable gRPC component, and pluggable state stores don't support transactional etags yet", sup.reason)
}

func TestLogSupport(t *testing.T) {
	newStore := func(t *testing.T, features []contribstate.Feature) *recordingStore {
		return &recordingStore{
			Backend:  inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name())).(Backend),
			features: features,
		}
	}

	tests := map[string]struct {
		store contribstate.Store
		want  string
	}{
		"strict with create-only": {
			store: strictStore{newStore(t, allFeatures)},
		},
		"no TRANSACTIONAL_ETAGS": {
			store: strictStore{newStore(t, nonStrictFeatures)},
			want:  "Actor state etag protection is off: actor state store 'mystore' doesn't support the TRANSACTIONAL_ETAGS feature",
		},
		"feature without MultiWithETags": {
			store: newStore(t, allFeatures),
			want:  "Actor state etag protection is off: actor state store 'mystore' advertises TRANSACTIONAL_ETAGS but doesn't implement MultiWithETags",
		},
		"no CREATE_ONLY": {
			store: strictStore{newStore(t, noCreateOnly)},
			want:  "Actor state create-only is off: actor state store 'mystore' doesn't support the CREATE_ONLY feature",
		},
		"not an actor state store": {
			store: strictStore{newStore(t, []contribstate.Feature{contribstate.FeatureTransactional})},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			logs := captureLog(t)
			LogSupport("mystore", test.store)
			if test.want == "" {
				assert.Empty(t, logs.String())
				return
			}
			assert.Contains(t, logs.String(), test.want)
			assert.Equal(t, 1, strings.Count(logs.String(), "level=warning"))
		})
	}
}

func TestLogOncePerStore(t *testing.T) {
	newSwitchable := func(t *testing.T, store Backend) (Interface, func(name string)) {
		t.Helper()
		cs := compstore.New()
		require.NoError(t, cs.AddStateStoreActor("mystore", store))
		s := New(Options{
			AppID:      "myapp",
			CompStore:  cs,
			Resiliency: resiliency.New(logger.NewLogger(t.Name())),
		})
		return s, func(name string) {
			cs.DeleteStateStore("mystore")
			require.NoError(t, cs.AddStateStoreActor(name, store))
		}
	}

	t.Run("missing etag", func(t *testing.T) {
		store := &recordingStore{
			Backend:  inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name())).(Backend),
			features: allFeatures,
		}
		s, switchTo := newSwitchable(t, strictStore{store})
		require.NoError(t, s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v1", nil)), false))
		store.noGetETags = true
		logs := captureLog(t)

		getStrict(t, s, "k")
		getStrict(t, s, "k")
		switchTo("otherstore")
		getStrict(t, s, "k")
		getStrict(t, s, "k")
		assert.Equal(t, 1, strings.Count(logs.String(), "store 'mystore' advertises"))
		assert.Equal(t, 1, strings.Count(logs.String(), "store 'otherstore' advertises"))
	})

	t.Run("rejected etag", func(t *testing.T) {
		store := &recordingStore{
			Backend:  inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name())).(Backend),
			features: nonStrictFeatures,
		}
		s, switchTo := newSwitchable(t, store)
		logs := captureLog(t)
		reject := func() {
			err := s.TransactionalStateOperation(t.Context(), true, txn(true, upsert("k", "v1", ptr.Of("1"))), false)
			assertAPIError(t, err, messages.ErrActorStateETagUnsupported, codes.FailedPrecondition, http.StatusBadRequest)
		}

		reject()
		reject()
		switchTo("otherstore")
		reject()
		reject()
		assert.Equal(t, 1, strings.Count(logs.String(), "actor state store 'mystore'"))
		assert.Equal(t, 1, strings.Count(logs.String(), "actor state store 'otherstore'"))
	})
}
