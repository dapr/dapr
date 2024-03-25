/*
Copyright 2024 The Dapr Authors
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

package metadata

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

const (
	daprdSPIFFEPrefix = "spiffe://public/ns/default/"
)

func init() {
	suite.Register(new(enabled))
}

// enabled tests that the metadata endpoint only responds to authenticated and
// authorized SPIFFE mTLS clients.
type enabled struct {
	daprd        *daprd.Daprd
	daprdConfig  *daprd.Daprd
	daprdCLI     *daprd.Daprd
	daprdNoAuthz *daprd.Daprd
	sentry       *sentry.Sentry
}

func (e *enabled) Setup(t *testing.T) []framework.Option {
	e.sentry = sentry.New(t)

	e.daprd = daprd.New(t,
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(e.sentry.CABundle().TrustAnchors),
		)),
		daprd.WithSentryAddress(e.sentry.Address()),
		daprd.WithEnableMTLS(true),
		daprd.WithMetadataAuthorizedIDs(
			"spiffe://public/ns/nsa/appida",
			"spiffe://public/ns/nsb/appidb",
		),
		daprd.WithConfigContents(`apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: metadata
spec:
  mtls:
    metadataAuthorizedIDs: ["spiffe://public/ns/nsc/appidc", "spiffe://public/ns/nsd/appidd"]
`))

	e.daprdConfig = daprd.New(t,
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(e.sentry.CABundle().TrustAnchors),
		)),
		daprd.WithSentryAddress(e.sentry.Address()),
		daprd.WithEnableMTLS(true),
		daprd.WithConfigContents(`apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: metadata
spec:
  mtls:
    metadataAuthorizedIDs: ["spiffe://public/ns/nsc/appidc", "spiffe://public/ns/nsd/appidd"]
`))

	e.daprdCLI = daprd.New(t,
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(e.sentry.CABundle().TrustAnchors),
		)),
		daprd.WithSentryAddress(e.sentry.Address()),
		daprd.WithEnableMTLS(true),
		daprd.WithMetadataAuthorizedIDs(
			"spiffe://public/ns/nsa/appida",
			"spiffe://public/ns/nsb/appidb",
		),
	)

	e.daprdNoAuthz = daprd.New(t,
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS", string(e.sentry.CABundle().TrustAnchors),
		)),
		daprd.WithSentryAddress(e.sentry.Address()),
		daprd.WithEnableMTLS(true),
	)

	return []framework.Option{
		framework.WithProcesses(e.sentry, e.daprd, e.daprdConfig, e.daprdCLI, e.daprdNoAuthz),
	}
}

func (e *enabled) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)
	e.daprdConfig.WaitUntilRunning(t, ctx)
	e.daprdCLI.WaitUntilRunning(t, ctx)
	e.daprdNoAuthz.WaitUntilRunning(t, ctx)

	var (
		tlsConfig *tls.Config
		resp      *http.Response
		client    = util.HTTPClient(t)
	)

	metaID, err := spiffeid.FromString(daprdSPIFFEPrefix + e.daprd.AppID())
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://localhost:%d/v1.0/metadata", e.daprd.MetadataPort()), nil)
	require.NoError(t, err)

	metaConfigID, err := spiffeid.FromString(daprdSPIFFEPrefix + e.daprdConfig.AppID())
	require.NoError(t, err)
	reqConfig, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://localhost:%d/v1.0/metadata", e.daprdConfig.MetadataPort()), nil)
	require.NoError(t, err)

	metaCLIID, err := spiffeid.FromString(daprdSPIFFEPrefix + e.daprdCLI.AppID())
	require.NoError(t, err)
	reqCLI, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://localhost:%d/v1.0/metadata", e.daprdCLI.MetadataPort()), nil)
	require.NoError(t, err)

	metaNoAuthzID, err := spiffeid.FromString(daprdSPIFFEPrefix + e.daprdNoAuthz.AppID())
	require.NoError(t, err)
	reqNoAuthz, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://localhost:%d/v1.0/metadata", e.daprdNoAuthz.MetadataPort()), nil)
	require.NoError(t, err)

	for _, nsappid := range []struct {
		ns       string
		appid    string
		configOK bool
		cliOK    bool
	}{
		{"nsa", "appida", false, true},
		{"nsb", "appidb", false, true},
		{"nsc", "appidc", true, false},
		{"nsd", "appidd", true, false},
	} {
		sec := e.sentry.Security(t, ctx, nsappid.ns, nsappid.appid)
		tlsConfig, err = sec.MTLSClientConfig(metaID)
		require.NoError(t, err)
		client.Transport = &http.Transport{TLSClientConfig: tlsConfig}
		resp, err = client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, resp.Body.Close())

		tlsConfig, err = sec.MTLSClientConfig(metaConfigID)
		require.NoError(t, err)
		client.Transport = &http.Transport{TLSClientConfig: tlsConfig}
		resp, err = client.Do(reqConfig)
		if nsappid.configOK {
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			require.NoError(t, resp.Body.Close())
		} else {
			require.ErrorContains(t, err, "remote error: tls: bad certificate")
		}

		tlsConfig, err = sec.MTLSClientConfig(metaCLIID)
		require.NoError(t, err)
		client.Transport = &http.Transport{TLSClientConfig: tlsConfig}
		resp, err = client.Do(reqCLI)
		if nsappid.cliOK {
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			require.NoError(t, resp.Body.Close())
		} else {
			require.ErrorContains(t, err, "remote error: tls: bad certificate")
		}

		tlsConfig, err = sec.MTLSClientConfig(metaNoAuthzID)
		require.NoError(t, err)
		client.Transport = &http.Transport{TLSClientConfig: tlsConfig}
		//nolint:bodyclose
		_, err = client.Do(reqNoAuthz)
		require.ErrorContains(t, err, "remote error: tls: bad certificate")
	}

	for _, nsappid := range []struct {
		ns    string
		appid string
	}{
		{"nsA", "appida"},
		{"nsb", "appidB"},
		{"nsc", "appidd"},
		{"nsa", "appidd"},
	} {
		sec := e.sentry.Security(t, ctx, nsappid.ns, nsappid.appid)
		tlsConfig, err = sec.MTLSClientConfig(metaID)
		require.NoError(t, err)
		client.Transport = &http.Transport{TLSClientConfig: tlsConfig}
		//nolint:bodyclose
		_, err = client.Do(req)
		require.ErrorContains(t, err, "remote error: tls: bad certificate")

		tlsConfig, err = sec.MTLSClientConfig(metaNoAuthzID)
		require.NoError(t, err)
		client.Transport = &http.Transport{TLSClientConfig: tlsConfig}
		//nolint:bodyclose
		_, err = client.Do(reqNoAuthz)
		require.ErrorContains(t, err, "remote error: tls: bad certificate")
	}

	client = util.HTTPClient(t)

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/v1.0/metadata", e.daprd.MetadataPort()), nil)
	require.NoError(t, err)
	reqNoAuthz, err = http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/v1.0/metadata", e.daprdNoAuthz.MetadataPort()), nil)
	require.NoError(t, err)
	for _, req := range []*http.Request{req, reqNoAuthz} {
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "Client sent an HTTP request to an HTTPS server.\n", string(body))
		require.NoError(t, resp.Body.Close())
	}
}
