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

package hostinjection

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(allowlist))
}

// allowlist asserts that with a concrete --oidc-allowed-hosts, an
// X-Forwarded-Host value that only looks like an allowed host (here, one
// carrying userinfo) is rejected rather than used to build the discovery
// document's issuer and jwks_uri.
type allowlist struct {
	sentry *sentry.Sentry
}

func (a *allowlist) Setup(t *testing.T) []framework.Option {
	a.sentry = sentry.New(t,
		sentry.WithMode("standalone"),
		sentry.WithEnableJWT(true),
		sentry.WithJWTTTL(2*time.Hour),
		sentry.WithOIDCEnabled(true),
		sentry.WithOIDCAllowedHosts([]string{"localhost"}),
	)

	return []framework.Option{
		framework.WithProcesses(a.sentry),
	}
}

func (a *allowlist) Run(t *testing.T, ctx context.Context) {
	a.sentry.WaitUntilRunning(t, ctx)

	url := fmt.Sprintf("http://localhost:%d/.well-known/openid-configuration", a.sentry.OIDCPort(t))
	client := &http.Client{Timeout: 10 * time.Second}

	discovery := func(t *testing.T, forwardedHost string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(t, err)
		req.Header.Set("X-Forwarded-Host", forwardedHost)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, body
	}

	t.Run("allowed X-Forwarded-Host builds issuer", func(t *testing.T) {
		status, body := discovery(t, "localhost:1234")
		require.Equal(t, http.StatusOK, status)

		var doc map[string]any
		require.NoError(t, json.Unmarshal(body, &doc))
		assert.Equal(t, "http://localhost:1234", doc["issuer"])
		assert.Equal(t, "http://localhost:1234/jwks.json", doc["jwks_uri"])
	})

	t.Run("X-Forwarded-Host with userinfo is rejected", func(t *testing.T) {
		const attacker = "attacker.example.com"
		status, body := discovery(t, "localhost:1@"+attacker)
		assert.Equal(t, http.StatusForbidden, status)
		assert.NotContains(t, string(body), attacker)
	})
}
