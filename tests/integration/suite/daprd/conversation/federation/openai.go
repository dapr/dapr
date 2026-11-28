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
	fos "github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/http/openai"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(openaiWIF))
}

// openaiWIF tests that the OpenAI conversation component authenticates with
// workload identity federation, exchanging a JWT SVID minted on demand by
// Sentry for the app's SPIFFE identity, with no API key.
type openaiWIF struct {
	sentry    *sentry.Sentry
	placement *placement.Placement
	scheduler *scheduler.Scheduler
	openai    *openai.OpenAI
	daprd     *daprd.Daprd
}

func (o *openaiWIF) Setup(t *testing.T) []framework.Option {
	// The fake OpenAI token endpoint is trusted through SSL_CERT_FILE, which
	// Go reads on Linux only.
	fos.SkipWindows(t)
	fos.SkipMacOS(t)

	o.sentry = sentry.New(t,
		sentry.WithMode("standalone"),
		sentry.WithEnableJWT(true),
		sentry.WithJWTIssuer(issuer),
	)
	bundle := o.sentry.CABundle()

	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, bundle.X509.TrustAnchors, 0o600))

	o.scheduler = scheduler.New(t,
		scheduler.WithSentry(o.sentry),
		scheduler.WithID("dapr-scheduler-server-0"),
	)
	o.placement = placement.New(t,
		placement.WithEnableTLS(true),
		placement.WithTrustAnchorsFile(taFile),
		placement.WithSentryAddress(o.sentry.Address()),
	)

	o.openai = openai.New(t, openai.WithJWKS(bundle.JWT.JWKS))

	o.daprd = daprd.New(t,
		daprd.WithAppID("wif-app"),
		daprd.WithMode("standalone"),
		daprd.WithNamespace("default"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(bundle.X509.TrustAnchors),
			"HTTPS_PROXY", o.openai.ProxyURL(),
			"SSL_CERT_FILE", o.openai.CAFile(),
		)),
		daprd.WithSentryAddress(o.sentry.Address()),
		daprd.WithPlacementAddresses(o.placement.Address()),
		daprd.WithSchedulerAddresses(o.scheduler.Address()),
		daprd.WithEnableMTLS(true),
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: gpt
spec:
  type: conversation.openai
  version: v1
  metadata:
  - name: endpoint
    value: %s
  - name: identityProviderId
    value: idp_test
  - name: serviceAccountId
    value: svc_acct_test
`, o.openai.Endpoint())),
	)

	return []framework.Option{
		framework.WithProcesses(o.sentry, o.placement, o.scheduler, o.openai, o.daprd),
	}
}

func (o *openaiWIF) Run(t *testing.T, ctx context.Context) {
	o.sentry.WaitUntilRunning(t, ctx)
	o.placement.WaitUntilRunning(t, ctx)
	o.scheduler.WaitUntilRunning(t, ctx)
	o.daprd.WaitUntilRunning(t, ctx)

	postURL := fmt.Sprintf("http://%s/v1.0-alpha2/conversation/gpt/converse", o.daprd.HTTPAddress())
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
		assert.Contains(t, string(body), "hello from openai")
	}

	assert.Equal(t, 2, o.openai.Completions())

	// The access token is reused until it nears expiry, so both requests share
	// one exchange.
	exchanges := o.openai.Exchanges()
	require.Len(t, exchanges, 1)
	assert.Equal(t, "idp_test", exchanges[0].IdentityProviderID)
	assert.Equal(t, "svc_acct_test", exchanges[0].ServiceAccountID)
	tkn := exchanges[0].Token
	assert.Equal(t, "spiffe://public/ns/default/wif-app", tkn.Subject())
	assert.Equal(t, issuer, tkn.Issuer())
	assert.Equal(t, []string{openai.Audience}, tkn.Audience())
	assert.Equal(t, 5*time.Minute, tkn.Expiration().Sub(tkn.IssuedAt()))
}
