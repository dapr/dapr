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
	"fmt"
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

func init() {
	suite.Register(new(disabled))
}

// disabled tests that the metadata endpoint is not encrypted and is
// unauthorized.
type disabled struct {
	daprd  *daprd.Daprd
	sentry *sentry.Sentry
}

func (d *disabled) Setup(t *testing.T) []framework.Option {
	d.sentry = sentry.New(t)

	d.daprd = daprd.New(t,
		daprd.WithExecOptions(exec.WithEnvVars("DAPR_TRUST_ANCHORS", string(d.sentry.CABundle().TrustAnchors))),
		daprd.WithSentryAddress(d.sentry.Address()),
		// mTLS is disabled
		daprd.WithEnableMTLS(false),
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
    trustDomain: "integration.dapr.io"
    metadataAuthorizedIDs: ["spiffe://public/ns/nsc/appidc", "spiffe://public/ns/nsd/appidd"]
`))

	return []framework.Option{
		framework.WithProcesses(d.sentry, d.daprd),
	}
}

func (d *disabled) Run(t *testing.T, ctx context.Context) {
	d.daprd.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/v1.0/metadata", d.daprd.MetadataPort()), nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	metaID, err := spiffeid.FromString(daprdSPIFFEPrefix + d.daprd.AppID())
	require.NoError(t, err)
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://localhost:%d/v1.0/metadata", d.daprd.MetadataPort()), nil)
	require.NoError(t, err)
	sec := d.sentry.Security(t, ctx, "nsa", "appida")
	tlsConfig, err := sec.MTLSClientConfig(metaID)
	require.NoError(t, err)
	client.Transport = &http.Transport{TLSClientConfig: tlsConfig}

	//nolint:bodyclose
	_, err = client.Do(req)
	require.ErrorContains(t, err, "http: server gave HTTP response to HTTPS client")
}
