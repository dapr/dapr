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
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(mcpserverchurn))
}

// mcpserverchurn is a regression test for the 60s hot-reload churn of
// secret-backed MCPServers. The operator resolves a secretKeyRef header into a
// base64+JSON value; daprd decodes it to plaintext to use the connection. The
// copy daprd keeps in compstore must remain the operator (encoded) form,
// otherwise every reconcile diffs the locally decoded value against the
// operator's encoded value and reloads the server. This test drives the same
// differ.AreSame(stored, operatorForm) comparison the 60s reconcile uses by
// re-sending an identical UPDATE event: with the fix daprd reports the update as
// a no-op; without it daprd closes and reloads the server.
type mcpserverchurn struct {
	daprd    *daprd.Daprd
	operator *operator.Operator
	logline  *logline.LogLine
}

func (m *mcpserverchurn) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)

	m.logline = logline.New(t, logline.WithStdoutLineContains(
		"MCPServer update skipped: no changes detected",
	))

	m.operator = operator.New(t,
		operator.WithSentry(sentry),
	)

	m.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithLogLevel("debug"),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(m.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors)),
			exec.WithStdout(m.logline.Stdout()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, m.operator, m.logline, m.daprd),
	}
}

func (m *mcpserverchurn) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	// Operator form of a secret-backed MCPServer: the Authorization header
	// carries a secretKeyRef plus the value the operator resolved for it, i.e.
	// a base64-encoded secret wrapped as a JSON string (see
	// pkg/operator/api/components.go getSecret). daprd in kubernetes mode
	// base64-decodes this inline value when loading the server.
	encoded, err := json.Marshal(base64.StdEncoding.EncodeToString([]byte("supersecret")))
	require.NoError(t, err)

	server := mcpserverapi.MCPServer{
		ObjectMeta: metav1.ObjectMeta{Name: "weather-mcp", Namespace: "default"},
		Spec: mcpserverapi.MCPServerSpec{
			// example.com is never dialed: no in-process workflow engine is wired
			// up here, so the connection is skipped. IgnoreErrors keeps daprd up.
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

	m.operator.AddMCPServers(server)
	m.operator.MCPServerUpdateEvent(t, ctx, &operator.MCPServerUpdateEvent{
		MCPServer: &server,
		EventType: operatorv1.ResourceEventType_CREATED,
	})

	// Wait until the server is loaded into compstore before re-sending it, so
	// the second event hits the existing-resource comparison path.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		servers := m.daprd.GetMetaMCPServers(c, ctx)
		assert.Len(c, servers, 1)
	}, 10*time.Second, 10*time.Millisecond)

	// Re-send the identical (operator-form) resource. This is the same
	// comparison the periodic reconcile performs. With the fix the stored copy
	// equals this resource and the update is a no-op.
	m.operator.MCPServerUpdateEvent(t, ctx, &operator.MCPServerUpdateEvent{
		MCPServer: &server,
		EventType: operatorv1.ResourceEventType_UPDATED,
	})

	m.logline.EventuallyFoundAll(t)
}
