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

package processor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/components-contrib/secretstores"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	rtmock "github.com/dapr/dapr/pkg/runtime/mock"
	"github.com/dapr/kit/logger"
)

// TestProcessMCPServerResolvesSecrets verifies that loading a secret-backed
// MCPServer resolves its secretKeyRef header into the stored copy. The
// hot-reload reconciler relies on this: it resolves an incoming spec the same
// way (ProcessMCPServerSecrets) before comparing it against this stored copy, so
// an unchanged secret-ref server is not reloaded on every reconcile tick.
func TestProcessMCPServerResolvesSecrets(t *testing.T) {
	proc, reg := newTestProc()

	reg.SecretStores().RegisterComponent(
		func(_ logger.Logger) secretstores.SecretStore {
			return rtmock.NewMockKubernetesStore()
		},
		"kubernetesMock",
	)

	require.NoError(t, proc.processComponentAndDependents(t.Context(), componentsapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "mysecretstore"},
		Spec: componentsapi.ComponentSpec{
			Type:    "secretstores.kubernetesMock",
			Version: "v1",
		},
	}))

	storeName := "mysecretstore"
	server := mcpserverapi.MCPServer{
		ObjectMeta: metav1.ObjectMeta{Name: "weather-mcp", Namespace: "test"},
		Spec: mcpserverapi.MCPServerSpec{
			IgnoreErrors: true,
			Endpoint: mcpserverapi.MCPEndpoint{
				StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{
					URL:  "http://example.com/mcp",
					Auth: &mcpserverapi.MCPAuth{SecretStore: &storeName},
					Headers: []commonapi.NameValuePair{{
						Name:         "Authorization",
						SecretKeyRef: commonapi.SecretKeyRef{Name: "mysecret", Key: "key1"},
					}},
				},
			},
		},
	}

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error)
	go func() { errCh <- proc.Process(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			require.Fail(t, "timeout waiting for processor to return")
		}
	})

	require.True(t, proc.AddPendingMCPServer(ctx, server))

	var stored mcpserverapi.MCPServer
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		s, ok := proc.compStore.GetMCPServer("weather-mcp")
		assert.True(c, ok)
		stored = s
	}, 5*time.Second, 10*time.Millisecond)

	storedHeader := stored.Spec.Endpoint.StreamableHTTP.Headers[0]
	assert.True(t, storedHeader.HasValue(), "stored MCPServer header must carry the resolved secret value")
	assert.Equal(t, "value1", storedHeader.Value.String())
	// secretKeyRef is retained so the reconciler can re-resolve the incoming spec.
	assert.Equal(t, "mysecret", storedHeader.SecretKeyRef.Name)
}
