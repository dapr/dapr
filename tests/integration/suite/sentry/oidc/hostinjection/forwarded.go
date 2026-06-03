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
	suite.Register(new(forwarded))
}

// forwarded asserts that an attacker-controlled X-Forwarded-Host header cannot
// poison the OIDC discovery document when Sentry is started without a static
// --jwt-issuer and without --oidc-allowed-hosts. The discovery document's
// issuer and jwks_uri must reflect the real request host, not the
// attacker-supplied header (CWE-346). This test fails against vulnerable code
// that trusts X-Forwarded-Host unconditionally in handleDiscovery.
type forwarded struct {
	sentry  *sentry.Sentry
	baseURL string
}

func (f *forwarded) Setup(t *testing.T) []framework.Option {
	f.sentry = sentry.New(t,
		sentry.WithMode("standalone"),
		sentry.WithEnableJWT(true),
		sentry.WithJWTTTL(2*time.Hour),
		sentry.WithOIDCEnabled(true),
	)

	return []framework.Option{
		framework.WithProcesses(f.sentry),
	}
}

func (f *forwarded) Run(t *testing.T, ctx context.Context) {
	f.sentry.WaitUntilRunning(t, ctx)

	port := f.sentry.OIDCPort(t)
	f.baseURL = fmt.Sprintf("http://localhost:%d", port)

	client := &http.Client{Timeout: 10 * time.Second}

	t.Run("baseline discovery has expected issuer", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			f.baseURL+"/.well-known/openid-configuration", nil)
		require.NoError(t, err)

		resp, err := client.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { resp.Body.Close() })

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var doc map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&doc))

		assert.Equal(t, f.baseURL, doc["issuer"],
			"baseline issuer should match request host")
		assert.Equal(t, f.baseURL+"/jwks.json", doc["jwks_uri"],
			"baseline jwks_uri should match request host")
	})

	t.Run("X-Forwarded-Host must not control issuer or jwks_uri", func(t *testing.T) {
		const attacker = "attacker.example.com"

		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			f.baseURL+"/.well-known/openid-configuration", nil)
		require.NoError(t, err)
		req.Header.Set("X-Forwarded-Host", attacker)

		resp, err := client.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { resp.Body.Close() })

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var doc map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&doc))

		// Discovery fields must come from the real request, not from a
		// client-supplied X-Forwarded-Host header. A relying party that performs
		// dynamic discovery trusts these values to locate the signing keys, so an
		// attacker who can set this header through a reverse proxy must not be
		// able to redirect that lookup.
		assert.NotContains(t, fmt.Sprint(doc["issuer"]), attacker,
			"issuer must not echo X-Forwarded-Host (CWE-346)")
		assert.NotContains(t, fmt.Sprint(doc["jwks_uri"]), attacker,
			"jwks_uri must not echo X-Forwarded-Host (CWE-346)")
		assert.Equal(t, f.baseURL, doc["issuer"],
			"issuer must reflect the real request host")
		assert.Equal(t, f.baseURL+"/jwks.json", doc["jwks_uri"],
			"jwks_uri must reflect the real request host")
	})
}
