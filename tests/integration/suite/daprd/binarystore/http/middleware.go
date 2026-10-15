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

package http

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(middleware))
}

// middleware proves the max body size limit is applied to the request body
// before the HTTP middleware pipeline runs, while binary store uploads (which
// opt out of the limit) still stream the full body through the pipeline. It
// uses the middleware.http.bodyprobe test component, which records how many
// body bytes it can read per request; a middleware that observed the raw
// unbounded body would record the full payload for every request.
//
// A second daprd denies the binarystore API to prove that the opt-out is only
// honoured for endpoints that are actually registered: a denied endpoint must
// not become a way to bypass the limit.
type middleware struct {
	daprd       *daprd.Daprd
	probeFile   string
	denied      *daprd.Daprd
	deniedProbe string
}

// maxBodySize mirrors the daprd --max-body-size value used below.
const maxBodySize = 1 << 20

const binarystoreYAML = `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: binarystore.in-memory
  version: v1
  metadata:
    - name: maxSize
      value: "8388608"
    - name: maxTotalSize
      value: "16777216"
`

func (m *middleware) Setup(t *testing.T) []framework.Option {
	m.probeFile = filepath.Join(t.TempDir(), "bodyprobe.log")
	m.deniedProbe = filepath.Join(t.TempDir(), "bodyprobe-denied.log")

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: bodyprobe
spec:
  httpPipeline:
    handlers:
      - name: bodyprobe
        type: middleware.http.bodyprobe
`), 0o600))

	deniedConfigFile := filepath.Join(t.TempDir(), "config-denied.yaml")
	require.NoError(t, os.WriteFile(deniedConfigFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: bodyprobe-denied
spec:
  api:
    denied:
      - name: binarystore
        version: v1alpha1
        protocol: http
  httpPipeline:
    handlers:
      - name: bodyprobe
        type: middleware.http.bodyprobe
`), 0o600))

	probeComponentYAML := func(probeFile string) string {
		return fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: bodyprobe
spec:
  type: middleware.http.bodyprobe
  version: v1
  metadata:
    - name: path
      value: %q
`, filepath.ToSlash(probeFile))
	}

	m.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourceFiles(probeComponentYAML(m.probeFile), binarystoreYAML),
		daprd.WithMaxBodySize("1Mi"),
	)
	m.denied = daprd.New(t,
		daprd.WithConfigs(deniedConfigFile),
		daprd.WithResourceFiles(probeComponentYAML(m.deniedProbe), binarystoreYAML),
		daprd.WithMaxBodySize("1Mi"),
	)
	return []framework.Option{
		framework.WithProcesses(m.daprd, m.denied),
	}
}

func (m *middleware) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)
	m.denied.WaitUntilRunning(t, ctx)
	httpClient := client.HTTP(t)

	type probeRecord struct {
		bytesRead int
		readErr   bool
	}

	// Records are "METHOD path bytesRead readErr" lines.
	lastProbeRecord := func(t *testing.T, probeFile, method, path string) probeRecord {
		t.Helper()

		content, err := os.ReadFile(probeFile)
		require.NoError(t, err)

		var found *probeRecord
		for line := range strings.Lines(string(content)) {
			fields := strings.Fields(line)
			if len(fields) != 4 || fields[0] != method || fields[1] != path {
				continue
			}
			bytesRead, err := strconv.Atoi(fields[2])
			require.NoError(t, err)
			readErr, err := strconv.ParseBool(fields[3])
			require.NoError(t, err)
			found = &probeRecord{bytesRead: bytesRead, readErr: readErr}
		}
		require.NotNil(t, found, "no bodyprobe record for %s %s", method, path)
		return *found
	}

	doRequest := func(t *testing.T, method, url string, body []byte) (int, string) {
		t.Helper()

		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(respBody)
	}

	oversized := bytes.Repeat([]byte{0xAB}, 2*maxBodySize)

	t.Run("pipeline middleware observes capped body on regular endpoints", func(t *testing.T) {
		// PUT metadata stores the raw body as the value, so a successful
		// request here would mean the handler saw an uncapped body.
		url := fmt.Sprintf("http://%s/v1.0/metadata/probe", m.daprd.HTTPAddress())
		status, body := doRequest(t, http.MethodPut, url, oversized)
		assert.Equal(t, http.StatusBadRequest, status)
		assert.Contains(t, body, "ERR_BODY_READ")

		rec := lastProbeRecord(t, m.probeFile, http.MethodPut, "/v1.0/metadata/probe")
		assert.Equal(t, maxBodySize, rec.bytesRead)
		assert.True(t, rec.readErr, "expected the middleware to hit the body size limit")

		// Within the limit, the same request succeeds through the probe.
		status, _ = doRequest(t, http.MethodPut, url, []byte("small"))
		assert.Equal(t, http.StatusNoContent, status)
		rec = lastProbeRecord(t, m.probeFile, http.MethodPut, "/v1.0/metadata/probe")
		assert.Equal(t, len("small"), rec.bytesRead)
		assert.False(t, rec.readErr)
	})

	t.Run("pipeline middleware observes full body on binarystore upload", func(t *testing.T) {
		payload := bytes.Repeat([]byte{0xCD}, maxBodySize+(512<<10))

		url := fmt.Sprintf("http://%s/v1.0-alpha1/binarystore/mystore/probe.bin", m.daprd.HTTPAddress())
		status, _ := doRequest(t, http.MethodPut, url, payload)
		require.Equal(t, http.StatusNoContent, status)

		rec := lastProbeRecord(t, m.probeFile, http.MethodPut, "/v1.0-alpha1/binarystore/mystore/probe.bin")
		assert.Equal(t, len(payload), rec.bytesRead)
		assert.False(t, rec.readErr)

		status, got := doRequest(t, http.MethodGet, url, nil)
		require.Equal(t, http.StatusOK, status)
		assert.Equal(t, payload, []byte(got))
	})

	t.Run("denied binarystore upload does not bypass the body size limit", func(t *testing.T) {
		payload := bytes.Repeat([]byte{0xEF}, maxBodySize+(512<<10))

		url := fmt.Sprintf("http://%s/v1.0-alpha1/binarystore/mystore/probe.bin", m.denied.HTTPAddress())
		status, _ := doRequest(t, http.MethodPut, url, payload)
		assert.NotEqual(t, http.StatusNoContent, status)

		rec := lastProbeRecord(t, m.deniedProbe, http.MethodPut, "/v1.0-alpha1/binarystore/mystore/probe.bin")
		assert.Equal(t, maxBodySize, rec.bytesRead)
		assert.True(t, rec.readErr, "expected the middleware to hit the body size limit")

		// Still limited on the other side too: nothing was stored.
		status, _ = doRequest(t, http.MethodGet, url, nil)
		assert.NotEqual(t, http.StatusOK, status)
	})
}
