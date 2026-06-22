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

package operator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(mcpserverreconcile))
}

// mcpserverreconcile is a regression test for the 60s hot-reload churn driven by
// the backup reconciler: the periodic reconcile lists all MCPServers from the
// operator and diffs them against the copy daprd holds in compstore. Using the
// --hot-reload-reconcile-interval flag the interval is shortened to 1s (the
// default is 60s) so the reconcile is exercised deterministically, mirroring the
// operator's --cache-sync-period test pattern. With the churn fix the stored
// copy keeps the operator (encoded) form, so each reconcile is a no-op; without
// it the reconcile reloads the server on every tick.
type mcpserverreconcile struct {
	daprd    *daprd.Daprd
	operator *operator.Operator
	daprdLog *log.Log
}

func (m *mcpserverreconcile) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)

	m.daprdLog = log.New()

	m.operator = operator.New(t,
		operator.WithSentry(sentry),
	)

	m.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithLogLevel("debug"),
		daprd.WithHotReloadReconcileInterval(time.Second),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(m.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors)),
			exec.WithStdout(m.daprdLog),
			exec.WithStderr(m.daprdLog),
		),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, m.operator, m.daprd),
	}
}

func (m *mcpserverreconcile) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	// Operator form of a secret-backed MCPServer: the Authorization header
	// carries a secretKeyRef plus the value the operator resolved for it (a
	// base64-encoded secret wrapped as a JSON string). daprd base64-decodes this
	// inline value when loading the server.
	encoded, err := json.Marshal(base64.StdEncoding.EncodeToString([]byte("supersecret")))
	require.NoError(t, err)

	server := mcpserverapi.MCPServer{
		ObjectMeta: metav1.ObjectMeta{Name: "weather-mcp", Namespace: "default"},
		Spec: mcpserverapi.MCPServerSpec{
			IgnoreErrors: true,
			Endpoint: mcpserverapi.MCPEndpoint{
				StreamableHTTP: &mcpserverapi.MCPStreamableHTTP{
					URL: "http://example.com/mcp",
					Headers: []commonapi.NameValuePair{{
						Name:         "Authorization",
						SecretKeyRef: commonapi.SecretKeyRef{Name: "weather-secret", Key: "token"},
						Value:        commonapi.DynamicValue{JSON: apiextv1.JSON{Raw: encoded}},
					}},
				},
			},
		},
	}

	// AddMCPServers makes the resource visible to the backup reconcile's
	// ListMCPServers; the event loads it into compstore initially.
	m.operator.AddMCPServers(server)
	m.operator.MCPServerUpdateEvent(t, ctx, &operator.MCPServerUpdateEvent{
		MCPServer: &server,
		EventType: operatorv1.ResourceEventType_CREATED,
	})

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		servers := m.daprd.GetMetaMCPServers(c, ctx)
		assert.Len(c, servers, 1)
	}, 10*time.Second, 10*time.Millisecond)

	// Confirm the backup reconcile actually runs in the window (interval is 1s),
	// so the assert.Never below is meaningful and not vacuous.
	require.Eventually(t, func() bool {
		return m.daprdLog.Contains("Running scheduled MCPServer reconcile")
	}, 10*time.Second, 10*time.Millisecond, "backup reconcile did not run")

	// Over several reconcile cycles the secret-backed server must never be
	// reloaded: the stored operator-form copy equals what ListMCPServers returns.
	assert.Never(t, func() bool {
		return m.daprdLog.Contains("Closing existing MCPServer to reload")
	}, 10*time.Second, 100*time.Millisecond,
		"secret-backed MCPServer was reloaded by the backup reconciler (churn)")
}
