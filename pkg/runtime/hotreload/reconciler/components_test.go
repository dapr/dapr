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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	inmemory "github.com/dapr/components-contrib/state/in-memory"
	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/modes"
	outboxfake "github.com/dapr/dapr/pkg/outbox/fake"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/authorizer"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/dapr/pkg/runtime/registry"
	securityfake "github.com/dapr/dapr/pkg/security/fake"
)

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
		m.update(ctx, actorStoreComp("mystore", false, 1))
		_, _, ok := cs.GetStateStoreActor()
		assert.False(t, ok)
		assert.Equal(t, int64(0), kicks.Load())

		// Update to marked: slot occupied. Both the state processor (add
		// side) and the reconciler (settle) notify; kicks coalesce in the
		// real runtime.
		m.update(ctx, actorStoreComp("mystore", true, 2))
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
		m.update(ctx, comp)
		_, name, ok = cs.GetStateStoreActor()
		require.True(t, ok)
		assert.Equal(t, "mystore", name)
		assert.Equal(t, int64(4), kicks.Load())

		// Delete: slot cleared, reconciler settle notifies.
		m.delete(ctx, comp)
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

		m.update(ctx, actorStoreComp("mystore", true, 1))
		m.update(ctx, actorStoreComp("otherstore", true, 1))

		_, name, ok := cs.GetStateStoreActor()
		require.True(t, ok)
		assert.Equal(t, "mystore", name)
		_, exists := cs.GetComponent("otherstore")
		assert.False(t, exists, "second actor state store must not be installed")
		assert.Equal(t, int64(2), kicks.Load())

		// An unmarked second store is unaffected by the guard.
		m.update(ctx, actorStoreComp("plainstore", false, 1))
		_, exists = cs.GetComponent("plainstore")
		assert.True(t, exists)
	})

	t.Run("skipped store is replayed when the actor state store is deleted", func(t *testing.T) {
		var kicks atomic.Int64
		m, proc, cs := newComponentsActorStoreManager(t, &kicks)
		ctx := runProc(t, proc)

		// A rename delivered create-before-delete: the new store is skipped
		// while the old one occupies the slot, and applied when the old one
		// is deleted.
		m.update(ctx, actorStoreComp("mystore", true, 1))
		m.update(ctx, actorStoreComp("otherstore", true, 1))
		_, exists := cs.GetComponent("otherstore")
		require.False(t, exists)

		m.delete(ctx, actorStoreComp("mystore", true, 2))

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

		m.update(ctx, actorStoreComp("mystore", true, 1))
		m.update(ctx, actorStoreComp("otherstore", true, 1))

		m.update(ctx, actorStoreComp("mystore", false, 2))

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

		m.update(ctx, actorStoreComp("mystore", true, 1))
		m.update(ctx, actorStoreComp("otherstore", true, 1))

		// The skipped store is unmarked before the slot frees; it installs as
		// a plain store and must not be replayed as the actor state store.
		m.update(ctx, actorStoreComp("otherstore", false, 2))
		m.delete(ctx, actorStoreComp("mystore", true, 2))

		_, _, ok := cs.GetStateStoreActor()
		assert.False(t, ok)
		_, exists := cs.GetComponent("otherstore")
		assert.True(t, exists)
	})
}
