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

package federation

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/http/anthropic"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(anthropicWIF))
}

const issuer = "https://sentry.example.com"

// anthropicWIF tests that the Anthropic conversation component authenticates
// with workload identity federation, exchanging JWT SVIDs minted on demand by
// Sentry for the app's SPIFFE identity, with no API key.
type anthropicWIF struct {
	sentry    *sentry.Sentry
	placement *placement.Placement
	scheduler *scheduler.Scheduler
	anthropic *anthropic.Anthropic
	daprd     *daprd.Daprd
}

func (a *anthropicWIF) Setup(t *testing.T) []framework.Option {
	a.sentry = sentry.New(t,
		sentry.WithMode("standalone"),
		sentry.WithEnableJWT(true),
		sentry.WithJWTIssuer(issuer),
	)
	bundle := a.sentry.CABundle()

	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, bundle.X509.TrustAnchors, 0o600))

	a.scheduler = scheduler.New(t,
		scheduler.WithSentry(a.sentry),
		scheduler.WithID("dapr-scheduler-server-0"),
	)
	a.placement = placement.New(t,
		placement.WithEnableTLS(true),
		placement.WithTrustAnchorsFile(taFile),
		placement.WithSentryAddress(a.sentry.Address()),
	)

	a.anthropic = anthropic.New(t, bundle.JWT.JWKS)

	a.daprd = daprd.New(t,
		daprd.WithAppID("wif-app"),
		daprd.WithMode("standalone"),
		daprd.WithNamespace("default"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(bundle.X509.TrustAnchors))),
		daprd.WithSentryAddress(a.sentry.Address()),
		daprd.WithPlacementAddresses(a.placement.Address()),
		daprd.WithSchedulerAddresses(a.scheduler.Address()),
		daprd.WithEnableMTLS(true),
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: claude
spec:
  type: conversation.anthropic
  version: v1
  metadata:
  - name: endpoint
    value: %s
  - name: federationRuleId
    value: fdrl_test
  - name: organizationId
    value: 00000000-0000-0000-0000-000000000000
  - name: serviceAccountId
    value: svac_test
`, a.anthropic.Endpoint())),
	)

	return []framework.Option{
		framework.WithProcesses(a.sentry, a.placement, a.scheduler, a.anthropic, a.daprd),
	}
}

func (a *anthropicWIF) Run(t *testing.T, ctx context.Context) {
	a.sentry.WaitUntilRunning(t, ctx)
	a.placement.WaitUntilRunning(t, ctx)
	a.scheduler.WaitUntilRunning(t, ctx)
	a.daprd.WaitUntilRunning(t, ctx)

	postURL := fmt.Sprintf("http://%s/v1.0-alpha2/conversation/claude/converse", a.daprd.HTTPAddress())
	httpClient := client.HTTP(t)

	for range 2 {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL,
			strings.NewReader(`{"inputs":[{"messages":[{"ofUser":{"content":[{"text":"hello"}]}}]}]}`))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
		assert.Contains(t, string(body), "hello from claude")
	}

	assert.Equal(t, 2, a.anthropic.Messages())

	// Each request exchanged a newly minted JWT SVID, which the fake would have
	// rejected had its jti been reused.
	exchanged := a.anthropic.Exchanged()
	require.Len(t, exchanged, 2)
	assert.NotEqual(t, exchanged[0].JwtID(), exchanged[1].JwtID())
	for _, tkn := range exchanged {
		assert.Equal(t, "spiffe://public/ns/default/wif-app", tkn.Subject())
		assert.Equal(t, issuer, tkn.Issuer())
		assert.Equal(t, []string{anthropic.Audience}, tkn.Audience())
		assert.Equal(t, 5*time.Minute, tkn.Expiration().Sub(tkn.IssuedAt()))
	}
}
