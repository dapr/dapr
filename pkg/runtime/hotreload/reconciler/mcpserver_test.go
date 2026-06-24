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
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/authorizer"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/meta"
	rtmock "github.com/dapr/dapr/pkg/runtime/mock"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/dapr/pkg/runtime/registry"
	securityfake "github.com/dapr/dapr/pkg/security/fake"
)

const mcpSecretStoreName = "mysecretstore"

// mcpServerSecretRef builds an MCPServer whose Authorization header resolves
// from a secretKeyRef in the mock secret store (key1 -> "value1"). When value is
// non-empty it is set as the (already-resolved) header value, modelling the copy
// stored in compstore after load.
func mcpServerSecretRef(value string) mcpserverapi.MCPServer {
	storeName := mcpSecretStoreName
	header := commonapi.NameValuePair{
		Name:         "Authorization",
		SecretKeyRef: commonapi.SecretKeyRef{Name: "mysecret", Key: "key1"},
	}
	if value != "" {
		header.Value = commonapi.DynamicValue{JSON: apiextv1.JSON{Raw: []byte(value)}}
	}
	return mcpserverapi.MCPServer{
		ObjectMeta: metav1.ObjectMeta{Name: "weather-mcp"},
		Spec: mcpserverapi.MCPServerSpec{
			IgnoreErrors: true,
			Endpoint: mcpserverapi.MCPEndpoint{
				StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{
					URL:     "http://example.com/mcp",
					Auth:    &mcpserverapi.MCPAuth{SecretStore: &storeName},
					Headers: []commonapi.NameValuePair{header},
				},
			},
		},
	}
}

func newMCPGuardManager(t *testing.T) (*mcpservers, *processor.Processor, *compstore.ComponentStore) {
	t.Helper()

	cs := compstore.New()
	cs.AddSecretStore(mcpSecretStoreName, rtmock.NewMockKubernetesStore())

	reg := registry.New(registry.NewOptions())
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

	m := &mcpservers{
		store: cs,
		proc:  proc,
		auth:  authorizer.New(authorizer.Options{ID: "id"}),
	}
	return m, proc, cs
}

// Test_mcpservers_update_resolvesIncomingBeforeCompare verifies the hot-reload
// guard: the incoming spec's secrets are resolved (on a copy) before comparing
// against the already-resolved stored copy, so an unchanged secret-ref server is
// not reloaded while a rotated secret value still is. This is what stops the
// backup reconcile from churning secret-backed MCPServers every tick.
func Test_mcpservers_update_resolvesIncomingBeforeCompare(t *testing.T) {
	t.Run("unchanged secret-ref server is not reloaded", func(t *testing.T) {
		m, proc, cs := newMCPGuardManager(t)
		ctx := t.Context()

		// Stored copy is the resolved form, as written on load.
		stored := mcpServerSecretRef("")
		proc.ProcessMCPServerSecrets(ctx, &stored)
		require.True(t, stored.Spec.Endpoint.StreamableHTTP.Headers[0].HasValue())
		cs.AddMCPServer(stored)

		// Reconcile delivers the raw operator-form spec (secretKeyRef, no value).
		// The guard resolves a copy, finds it equal, and skips. Without the guard
		// update would delete the server and block on AddPendingMCPServer.
		done := make(chan struct{})
		go func() {
			defer close(done)
			m.update(ctx, mcpServerSecretRef(""))
		}()
		select {
		case <-done:
		case <-time.After(time.Second * 10):
			t.Fatal("update did not return: the no-op was treated as a reload")
		}

		got, ok := cs.GetMCPServer("weather-mcp")
		require.True(t, ok)
		assert.True(t, differ.AreSame(stored, got), "secret-ref server must be left untouched")
	})

	t.Run("rotated secret value triggers a reload", func(t *testing.T) {
		m, proc, cs := newMCPGuardManager(t)
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

		// Stored copy holds a stale resolved value; the live secret is "value1".
		cs.AddMCPServer(mcpServerSecretRef("stalevalue"))

		m.update(ctx, mcpServerSecretRef(""))

		// The guard resolves the incoming spec to "value1", sees it differs from
		// the stale stored value, and reloads: the processor re-stores "value1".
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			got, ok := cs.GetMCPServer("weather-mcp")
			assert.True(c, ok)
			if ok {
				assert.Equal(c, "value1", got.Spec.Endpoint.StreamableHTTP.Headers[0].Value.String())
			}
		}, time.Second*10, time.Millisecond*50)
	})
}
