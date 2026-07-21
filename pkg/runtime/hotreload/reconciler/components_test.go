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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/modes"
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

func newComponentsGuardManager(t *testing.T) (*components, *processor.Processor, *compstore.ComponentStore, *daprt.MockPubSub) {
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
	})

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

// Test_components_update_generationGuard pins the behaviour of the
// lower-generation reject in components.update, including the delete-then-stale
// reorder case raised in review.
func Test_components_update_generationGuard(t *testing.T) {
	t.Run("rejects a stale lower-generation update for an existing component", func(t *testing.T) {
		m, proc, cs, mockPubSub := newComponentsGuardManager(t)
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
		m, proc, cs, mockPubSub := newComponentsGuardManager(t)
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
