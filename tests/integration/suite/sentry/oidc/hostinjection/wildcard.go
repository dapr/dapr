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
	suite.Register(new(wildcard))
}

// wildcard asserts that a wildcard --oidc-allowed-hosts="*" does not re-open
// the X-Forwarded-Host injection path. The allowed-hosts middleware lets any
// host through under a wildcard, so handleDiscovery must still derive the
// issuer from r.Host rather than the attacker-supplied header (CWE-346).
type wildcard struct {
	sentry  *sentry.Sentry
	baseURL string
}

func (w *wildcard) Setup(t *testing.T) []framework.Option {
	w.sentry = sentry.New(t,
		sentry.WithMode("standalone"),
		sentry.WithEnableJWT(true),
		sentry.WithJWTTTL(2*time.Hour),
		sentry.WithOIDCEnabled(true),
		sentry.WithOIDCAllowedHosts([]string{"*"}),
	)

	return []framework.Option{
		framework.WithProcesses(w.sentry),
	}
}

func (w *wildcard) Run(t *testing.T, ctx context.Context) {
	w.sentry.WaitUntilRunning(t, ctx)

	port := w.sentry.OIDCPort(t)
	w.baseURL = fmt.Sprintf("http://localhost:%d", port)

	client := &http.Client{Timeout: 10 * time.Second}

	const attacker = "attacker.example.com"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		w.baseURL+"/.well-known/openid-configuration", nil)
	require.NoError(t, err)
	req.Header.Set("X-Forwarded-Host", attacker)

	resp, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })

	require.Equal(t, http.StatusOK, resp.StatusCode,
		"wildcard allowlist must allow the request through the middleware")

	var doc map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&doc))

	assert.NotContains(t, fmt.Sprint(doc["issuer"]), attacker,
		"wildcard allowlist must not re-open X-Forwarded-Host injection (CWE-346)")
	assert.NotContains(t, fmt.Sprint(doc["jwks_uri"]), attacker,
		"wildcard allowlist must not re-open X-Forwarded-Host injection (CWE-346)")
	assert.Equal(t, w.baseURL, doc["issuer"],
		"issuer must reflect the real request host even under a wildcard allowlist")
	assert.Equal(t, w.baseURL+"/jwks.json", doc["jwks_uri"],
		"jwks_uri must reflect the real request host even under a wildcard allowlist")
}
