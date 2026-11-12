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

package reconciler

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	inmemory "github.com/dapr/components-contrib/state/in-memory"
	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/modes"
	outboxfake "github.com/dapr/dapr/pkg/outbox/fake"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/authorizer"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/dapr/pkg/runtime/registry"
	securityfake "github.com/dapr/dapr/pkg/security/fake"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

// guardComp builds a pubsub component carrying a generation and a marker
// metadata value. The marker is part of the spec, so two components with
// different markers are not AreSame: only the generation guard stops the lower
// one from reloading.
func guardComp(gen int64, marker string) compapi.Component {
	return compapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "gpubsub", Generation: gen},
		Spec: compapi.ComponentSpec{
			Type:    "pubsub.mockPubSub",
			Version: "v1",
			Metadata: []commonapi.NameValuePair{{
				Name:  "marker",
				Value: commonapi.DynamicValue{JSON: apiextv1.JSON{Raw: []byte(marker)}},
			}},
		},
	}
}

// newComponentsManager builds a components manager backed by a real processor.
// In KubernetesMode an operator client is set, which is what makes the secret
// processor treat secretKeyRef values as operator-populated and base64 decode
// them in place.
func newComponentsManager(t *testing.T, mode modes.DaprMode) (*components, *processor.Processor, *compstore.ComponentStore, *daprt.MockPubSub) {
	t.Helper()

	cs := compstore.New()
	reg := registry.New(registry.NewOptions())

	mockPubSub := new(daprt.MockPubSub)
	reg.PubSubs().RegisterComponent(
		func(logger.Logger) contribpubsub.PubSub { return mockPubSub },
		"mockPubSub",
	)
	mockPubSub.On("Init", mock.Anything).Return(nil)
	mockPubSub.On("Close").Return(nil)

	opts := processor.Options{
		ID:             "id",
		Namespace:      "test",
		Registry:       reg,
		ComponentStore: cs,
		Meta:           meta.New(meta.Options{ID: "id", Namespace: "test", Mode: mode}),
		Resiliency:     resiliency.New(log),
		Mode:           mode,
		Channels:       new(channels.Channels),
		GlobalConfig:   new(config.Configuration),
		Security:       securityfake.New(),
		Reporter:       reg.Reporter(),
	}
	if mode == modes.KubernetesMode {
		opts.OperatorClient = operatorv1.NewOperatorClient(nil)
	}

	proc := processor.New(opts)

	m := &components{
		store: cs,
		proc:  proc,
		auth:  authorizer.New(authorizer.Options{ID: "id"}),
	}
	return m, proc, cs, mockPubSub
}

// runProc starts the processor loop and registers cleanup. update/delete block
// on the loop, so it must be running.
func runProc(t *testing.T, proc *processor.Processor) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() { errCh <- proc.Process(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(time.Second * 5):
			t.Error("processor did not return in time")
		}
	})
	return ctx
}

// actorStoreComp builds a state store component, optionally marked as the
// actor state store.
func actorStoreComp(name string, marked bool, gen int64) compapi.Component {
	comp := compapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: name, Generation: gen},
		Spec: compapi.ComponentSpec{
			Type:    "state.in-memory",
			Version: "v1",
		},
	}
	if marked {
		comp.Spec.Metadata = []commonapi.NameValuePair{{
			Name:  "actorStateStore",
			Value: commonapi.DynamicValue{JSON: apiextv1.JSON{Raw: []byte(`"true"`)}},
		}}
	}
	return comp
}

// boolSecretStore serves every secret with the keys "true" and "false", each
// holding its own name, so a secretKeyRef picks the resolved value by key.
type boolSecretStore struct {
	daprt.FakeSecretStore
}

func (boolSecretStore) GetSecret(context.Context, secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
	return secretstores.GetSecretResponse{
		Data: map[string]string{"true": "true", "false": "false"},
	}, nil
}

func newComponentsActorStoreManager(t *testing.T, kicks *atomic.Int64) (*components, *processor.Processor, *compstore.ComponentStore) {
	t.Helper()

	cs := compstore.New()
	reg := registry.New(registry.NewOptions())

	reg.StateStores().RegisterComponent(
		inmemory.NewInMemoryStateStore,
		"in-memory",
	)

	proc := processor.New(processor.Options{
		ID:             "id",
		Namespace:      "test",
		Registry:       reg,
		ComponentStore: cs,
		Meta:           meta.New(meta.Options{ID: "id", Namespace: "test", Mode: modes.StandaloneMode}),
		Resiliency:     resiliency.New(log),
		Mode:           modes.StandaloneMode,
		Channels:       new(channels.Channels),
		GlobalConfig:   new(config.Configuration),
		Security:       securityfake.New(),
		Reporter:       reg.Reporter(),
		Outbox:         outboxfake.New(),
		ActorsEnabled:  true,
		Actors: actorsfake.New().WithOnActorStateStoreChanged(func() {
			kicks.Add(1)
		}),
	})

	m := &components{
		store: cs,
		proc:  proc,
		auth:  authorizer.New(authorizer.Options{ID: "id"}),
	}
	return m, proc, cs
}

// Test_components_actorStateStore verifies the actor state store can be hot
// reloaded: added, updated in place, and deleted, with change notifications
// kicked to the actor runtime; and that a second actor state store is
// skipped.
func Test_components_actorStateStore(t *testing.T) {
	t.Run("add, update and delete the actor state store", func(t *testing.T) {
		var kicks atomic.Int64
		m, proc, cs := newComponentsActorStoreManager(t, &kicks)
		ctx := runProc(t, proc)

		// Add: an unmarked store does not occupy the actor slot or kick.
		require.NoError(t, m.update(ctx, actorStoreComp("mystore", false, 1)))
		_, _, ok := cs.GetStateStoreActor()
		assert.False(t, ok)
		assert.Equal(t, int64(0), kicks.Load())

		// Update to marked: slot occupied. Both the state processor (add
		// side) and the reconciler (settle) notify; kicks coalesce in the
		// real runtime.
		require.NoError(t, m.update(ctx, actorStoreComp("mystore", true, 2)))
		_, name, ok := cs.GetStateStoreActor()
		require.True(t, ok)
		assert.Equal(t, "mystore", name)
		assert.Equal(t, int64(2), kicks.Load())

		// Same-name update: the close side does not notify, so the actor
		// runtime never observes the transient store-less state between the
		// close and re-init; the re-init and reconciler settle notify.
		comp := actorStoreComp("mystore", true, 3)
		comp.Spec.Metadata = append(comp.Spec.Metadata, commonapi.NameValuePair{
			Name:  "marker",
			Value: commonapi.DynamicValue{JSON: apiextv1.JSON{Raw: []byte(`"x"`)}},
		})
		require.NoError(t, m.update(ctx, comp))
		_, name, ok = cs.GetStateStoreActor()
		require.True(t, ok)
		assert.Equal(t, "mystore", name)
		assert.Equal(t, int64(4), kicks.Load())

		// Delete: slot cleared, reconciler settle notifies.
		require.NoError(t, m.delete(ctx, comp))
		_, _, ok = cs.GetStateStoreActor()
		assert.False(t, ok)
		_, exists := cs.GetComponent("mystore")
		assert.False(t, exists)
		assert.Equal(t, int64(5), kicks.Load())
	})

	t.Run("a second actor state store is skipped", func(t *testing.T) {
		var kicks atomic.Int64
		m, proc, cs := newComponentsActorStoreManager(t, &kicks)
		ctx := runProc(t, proc)

		require.NoError(t, m.update(ctx, actorStoreComp("mystore", true, 1)))
		require.NoError(t, m.update(ctx, actorStoreComp("otherstore", true, 1)))

		_, name, ok := cs.GetStateStoreActor()
		require.True(t, ok)
		assert.Equal(t, "mystore", name)
		_, exists := cs.GetComponent("otherstore")
		assert.False(t, exists, "second actor state store must not be installed")
		assert.Equal(t, int64(2), kicks.Load())

		// An unmarked second store is unaffected by the guard.
		require.NoError(t, m.update(ctx, actorStoreComp("plainstore", false, 1)))
		_, exists = cs.GetComponent("plainstore")
		assert.True(t, exists)
	})

	t.Run("a second store with a falsy actorStateStore value is installed", func(t *testing.T) {
		var kicks atomic.Int64
		m, proc, cs := newComponentsActorStoreManager(t, &kicks)
		ctx := runProc(t, proc)

		require.NoError(t, m.update(ctx, actorStoreComp("mystore", true, 1)))

		for _, value := range []string{`"false"`, `false`, `"0"`, `""`} {
			comp := actorStoreComp("plainstore", false, 1)
			comp.Spec.Metadata = []commonapi.NameValuePair{{
				Name:  "actorStateStore",
				Value: commonapi.DynamicValue{JSON: apiextv1.JSON{Raw: []byte(value)}},
			}}
			require.NoError(t, m.update(ctx, comp))
			_, exists := cs.GetComponent("plainstore")
			assert.Truef(t, exists, "store with actorStateStore=%s must be installed", value)
			require.NoError(t, m.delete(ctx, comp))
		}

		_, name, ok := cs.GetStateStoreActor()
		require.True(t, ok)
		assert.Equal(t, "mystore", name)
	})

	// The guard must agree with the state processor on whether a second store
	// is marked: skipping an unmarked store loses it, and installing a marked
	// one fails its init with a duplicate actor state store and exits daprd.
	t.Run("a second store is skipped only when the processor would mark it", func(t *testing.T) {
		literal := func(v string) commonapi.NameValuePair {
			return commonapi.NameValuePair{
				Name:  "actorStateStore",
				Value: commonapi.DynamicValue{JSON: apiextv1.JSON{Raw: []byte(strconv.Quote(v))}},
			}
		}
		envRef := func(env string) commonapi.NameValuePair {
			return commonapi.NameValuePair{Name: "actorStateStore", EnvRef: env}
		}
		secretRef := func(key string) commonapi.NameValuePair {
			return commonapi.NameValuePair{
				Name:         "actorStateStore",
				SecretKeyRef: commonapi.SecretKeyRef{Name: "actorstore-secret", Key: key},
			}
		}
		withValue := func(nv commonapi.NameValuePair, v string) commonapi.NameValuePair {
			nv.Value = literal(v).Value
			return nv
		}

		t.Setenv("RECONCILER_TEST_ACTOR_STORE_FALSE", "false")
		t.Setenv("RECONCILER_TEST_ACTOR_STORE_TRUE", "true")

		tests := map[string]struct {
			metadata []commonapi.NameValuePair
			// secretStore loads the secret store the secretRef entries read.
			secretStore bool
			skipped     bool
		}{
			"duplicate entries, the last one is truthy": {
				metadata: []commonapi.NameValuePair{literal("false"), literal("true")},
				skipped:  true,
			},
			"duplicate entries, the last one is falsy": {
				metadata: []commonapi.NameValuePair{literal("true"), literal("false")},
			},
			"envRef resolving to false": {
				metadata: []commonapi.NameValuePair{envRef("RECONCILER_TEST_ACTOR_STORE_FALSE")},
			},
			"envRef resolving to true": {
				metadata: []commonapi.NameValuePair{envRef("RECONCILER_TEST_ACTOR_STORE_TRUE")},
				skipped:  true,
			},
			"envRef to an unset variable": {
				metadata: []commonapi.NameValuePair{envRef("RECONCILER_TEST_ACTOR_STORE_UNSET")},
			},
			"literal false next to an envRef resolving to true": {
				metadata: []commonapi.NameValuePair{withValue(envRef("RECONCILER_TEST_ACTOR_STORE_TRUE"), "false")},
			},
			"secretKeyRef resolving to false": {
				metadata:    []commonapi.NameValuePair{secretRef("false")},
				secretStore: true,
			},
			"secretKeyRef resolving to true": {
				metadata:    []commonapi.NameValuePair{secretRef("true")},
				secretStore: true,
				skipped:     true,
			},
			// The value cannot be known yet, so the store is treated as
			// marked rather than risking a second actor state store.
			"secretKeyRef to a secret store that is not loaded": {
				metadata: []commonapi.NameValuePair{secretRef("false")},
				skipped:  true,
			},
			// Resolution stops at the first secret whose store is not loaded,
			// so a later envRef is not resolved either.
			"envRef after a secret whose store is not loaded": {
				metadata: []commonapi.NameValuePair{
					{Name: "password", SecretKeyRef: commonapi.SecretKeyRef{Name: "actorstore-secret", Key: "false"}},
					envRef("RECONCILER_TEST_ACTOR_STORE_TRUE"),
				},
				skipped: true,
			},
			// The secret replaces the literal value once its store is loaded.
			"literal false with a secretKeyRef whose store is not loaded": {
				metadata: []commonapi.NameValuePair{withValue(secretRef("true"), "false")},
				skipped:  true,
			},
			// Names differing only in case are separate properties; the
			// processor marks the store if any of them is truthy.
			"case-variant names, one is truthy": {
				metadata: []commonapi.NameValuePair{
					literal("false"),
					{Name: "ACTORSTATESTORE", Value: literal("true").Value},
				},
				skipped: true,
			},
			// The processor keeps only the last entry of a name, so an earlier
			// unresolved reference does not decide anything.
			"unresolved secretKeyRef shadowed by a later literal false": {
				metadata:    []commonapi.NameValuePair{secretRef("missing"), literal("false")},
				secretStore: true,
			},
			"unresolved secretKeyRef after an earlier literal false": {
				metadata:    []commonapi.NameValuePair{literal("false"), secretRef("missing")},
				secretStore: true,
				skipped:     true,
			},
			// Placeholders are expanded by the processor at init, and {appID}
			// or {podName} may expand to a truthy value, so they count as marked.
			"appID placeholder": {
				metadata: []commonapi.NameValuePair{literal("{appID}")},
				skipped:  true,
			},
			"podName placeholder": {
				metadata: []commonapi.NameValuePair{literal("{podName}")},
				skipped:  true,
			},
		}

		for name, test := range tests {
			t.Run(name, func(t *testing.T) {
				var kicks atomic.Int64
				m, proc, cs := newComponentsActorStoreManager(t, &kicks)
				ctx := runProc(t, proc)

				if test.secretStore {
					cs.AddSecretStore("actorstore-secrets", boolSecretStore{})
				}

				require.NoError(t, m.update(ctx, actorStoreComp("mystore", true, 1)))

				comp := actorStoreComp("otherstore", false, 1)
				comp.SecretStore = "actorstore-secrets"
				comp.Spec.Metadata = test.metadata
				require.NoError(t, m.update(ctx, comp))

				_, exists := cs.GetComponent("otherstore")
				assert.Equal(t, !test.skipped, exists)
				// A component parked behind a secret store that is not loaded
				// is not installed either, so check it was skipped by the guard.
				m.skippedActorStoreLock.Lock()
				assert.Equal(t, test.skipped, m.skippedActorStore != nil)
				m.skippedActorStoreLock.Unlock()
				_, name, ok := cs.GetStateStoreActor()
				require.True(t, ok)
				assert.Equal(t, "mystore", name)
			})
		}
	})

	t.Run("skipped store is replayed when the actor state store is deleted", func(t *testing.T) {
		var kicks atomic.Int64
		m, proc, cs := newComponentsActorStoreManager(t, &kicks)
		ctx := runProc(t, proc)

		// A rename delivered create-before-delete: the new store is skipped
		// while the old one occupies the slot, and applied when the old one
		// is deleted.
		require.NoError(t, m.update(ctx, actorStoreComp("mystore", true, 1)))
		require.NoError(t, m.update(ctx, actorStoreComp("otherstore", true, 1)))
		_, exists := cs.GetComponent("otherstore")
		require.False(t, exists)

		require.NoError(t, m.delete(ctx, actorStoreComp("mystore", true, 2)))

		_, name, ok := cs.GetStateStoreActor()
		require.True(t, ok)
		assert.Equal(t, "otherstore", name)
		_, exists = cs.GetComponent("otherstore")
		assert.True(t, exists)
		_, exists = cs.GetComponent("mystore")
		assert.False(t, exists)
	})

	t.Run("skipped store is replayed when the actor state store is unmarked", func(t *testing.T) {
		var kicks atomic.Int64
		m, proc, cs := newComponentsActorStoreManager(t, &kicks)
		ctx := runProc(t, proc)

		require.NoError(t, m.update(ctx, actorStoreComp("mystore", true, 1)))
		require.NoError(t, m.update(ctx, actorStoreComp("otherstore", true, 1)))

		require.NoError(t, m.update(ctx, actorStoreComp("mystore", false, 2)))

		_, name, ok := cs.GetStateStoreActor()
		require.True(t, ok)
		assert.Equal(t, "otherstore", name)
		_, exists := cs.GetComponent("mystore")
		assert.True(t, exists, "unmarked store remains installed")
	})

	t.Run("a newer event supersedes the skipped store", func(t *testing.T) {
		var kicks atomic.Int64
		m, proc, cs := newComponentsActorStoreManager(t, &kicks)
		ctx := runProc(t, proc)

		require.NoError(t, m.update(ctx, actorStoreComp("mystore", true, 1)))
		require.NoError(t, m.update(ctx, actorStoreComp("otherstore", true, 1)))

		// The skipped store is unmarked before the slot frees; it installs as
		// a plain store and must not be replayed as the actor state store.
		require.NoError(t, m.update(ctx, actorStoreComp("otherstore", false, 2)))
		require.NoError(t, m.delete(ctx, actorStoreComp("mystore", true, 2)))

		_, _, ok := cs.GetStateStoreActor()
		assert.False(t, ok)
		_, exists := cs.GetComponent("otherstore")
		assert.True(t, exists)
	})
}

// Test_components_update_generationGuard pins the behaviour of the
// lower-generation reject in components.update, including the delete-then-stale
// reorder case raised in review.
func Test_components_update_generationGuard(t *testing.T) {
	t.Run("rejects a stale lower-generation update for an existing component", func(t *testing.T) {
		m, proc, cs, mockPubSub := newComponentsManager(t, modes.StandaloneMode)
		ctx := runProc(t, proc)

		// update blocks until the component is committed.
		require.NoError(t, m.update(ctx, guardComp(5, "v5")))
		got, ok := cs.GetComponent("gpubsub")
		require.True(t, ok)
		require.Equal(t, int64(5), got.GetGeneration())

		// A later event carrying a lower generation (and a different spec) must be
		// rejected: no close, no re-init, store unchanged.
		require.NoError(t, m.update(ctx, guardComp(4, "v4")))

		got, ok = cs.GetComponent("gpubsub")
		require.True(t, ok)
		assert.Equal(t, int64(5), got.GetGeneration(), "stale event must not downgrade the installed generation")
		assert.Equal(t, "v5", got.Spec.Metadata[0].Value.String(), "stale event must not overwrite the installed spec")
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub.AssertNumberOfCalls(t, "Close", 0)
	})

	t.Run("a stale update after a delete reinstalls the component (no tombstone)", func(t *testing.T) {
		// Documents the current behaviour: the generation guard lives inside the
		// `exists` branch, so once a delete clears the store a late lower
		// generation hits exists==false and is reinstalled. Closing this fully
		// would require tracking the last-seen generation across deletes.
		m, proc, cs, mockPubSub := newComponentsManager(t, modes.StandaloneMode)
		ctx := runProc(t, proc)

		require.NoError(t, m.update(ctx, guardComp(5, "v5")))
		require.NoError(t, m.delete(ctx, guardComp(6, "v5")))
		_, ok := cs.GetComponent("gpubsub")
		require.False(t, ok, "component should be gone after delete")

		require.NoError(t, m.update(ctx, guardComp(4, "v4")))
		got, ok := cs.GetComponent("gpubsub")
		require.True(t, ok)
		assert.Equal(t, int64(4), got.GetGeneration(), "current behaviour: late update reinstalls after delete")
		mockPubSub.AssertNumberOfCalls(t, "Init", 2)
		mockPubSub.AssertNumberOfCalls(t, "Close", 1)
	})
}
