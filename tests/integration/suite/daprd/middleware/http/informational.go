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
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"net/http/httptrace"
	"net/textproto"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(informational))
}

// informationalGuest is the binary encoding of this http-wasm guest, which
// answers every request itself with two 103s followed by a final 201:
//
//	(module
//	  (import "http_handler" "set_status_code" (func $set_status_code (param i32)))
//	  (import "http_handler" "write_body" (func $write_body (param $kind i32) (param $buf i32) (param $len i32)))
//	  (memory (export "memory") 1 1)
//	  (data (i32.const 0) "created")
//	  (func (export "handle_request") (result i64)
//	    (call $set_status_code (i32.const 103))
//	    (call $set_status_code (i32.const 103))
//	    (call $set_status_code (i32.const 201))
//	    (call $write_body (i32.const 1) (i32.const 0) (i32.const 7))
//	    (i64.const 0))
//	  (func (export "handle_response") (param $reqCtx i32) (param $isError i32)))
const informationalGuest = "\x00asm\x01\x00\x00\x00" +
	// Type section.
	"\x01\x14\x04" + "\x60\x01\x7f\x00" + "\x60\x03\x7f\x7f\x7f\x00" + "\x60\x00\x01\x7e" + "\x60\x02\x7f\x7f\x00" +
	// Import section.
	"\x02\x3a\x02" + "\x0chttp_handler\x0fset_status_code\x00\x00" + "\x0chttp_handler\x0awrite_body\x00\x01" +
	// Function section.
	"\x03\x03\x02\x02\x03" +
	// Memory section.
	"\x05\x04\x01\x01\x01\x01" +
	// Export section.
	"\x07\x2d\x03" + "\x06memory\x02\x00" + "\x0ehandle_request\x00\x02" + "\x0fhandle_response\x00\x03" +
	// Code section.
	"\x0a\x20\x02" +
	"\x1b\x00" + "\x41\xe7\x00\x10\x00" + "\x41\xe7\x00\x10\x00" + "\x41\xc9\x01\x10\x00" + "\x41\x01\x41\x00\x41\x07\x10\x01" + "\x42\x00\x0b" +
	"\x02\x00\x0b" +
	// Data section.
	"\x0b\x0d\x01\x00\x41\x00\x0b\x07created"

type informational struct {
	daprd *daprd.Daprd
}

func (i *informational) Setup(t *testing.T) []framework.Option {
	guestPath := filepath.Join(t.TempDir(), "informational.wasm")
	require.NoError(t, os.WriteFile(guestPath, []byte(informationalGuest), 0o600))

	i.daprd = daprd.New(t,
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: informational
spec:
  httpPipeline:
    handlers:
      - name: informational
        type: middleware.http.wasm
`),
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: informational
spec:
  type: middleware.http.wasm
  version: v1
  metadata:
  - name: url
    value: "file://%s"
`, guestPath)),
	)

	return []framework.Option{
		framework.WithProcesses(i.daprd),
	}
}

func (i *informational) Run(t *testing.T, ctx context.Context) {
	// The guest answers every route, including /v1.0/healthz, so poll the
	// request under test rather than waiting for daprd to report healthy.
	httpClient := client.HTTP(t)
	url := fmt.Sprintf("http://%s/v1.0/metadata", i.daprd.HTTPAddress())
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		var hints []int
		trace := &httptrace.ClientTrace{
			Got1xxResponse: func(code int, _ textproto.MIMEHeader) error {
				hints = append(hints, code)
				return nil
			},
		}
		req, err := nethttp.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), nethttp.MethodGet, url, nil)
		require.NoError(c, err)
		resp, err := httpClient.Do(req)
		if !assert.NoError(c, err) {
			return
		}
		body, err := io.ReadAll(resp.Body)
		require.NoError(c, err)
		require.NoError(c, resp.Body.Close())
		assert.Equal(c, nethttp.StatusCreated, resp.StatusCode)
		assert.Equal(c, []int{nethttp.StatusEarlyHints, nethttp.StatusEarlyHints}, hints)
		assert.Equal(c, "created", string(body))
	}, time.Second*20, time.Millisecond*10)

	// The metrics middleware records the status held by daprd's response
	// writer wrapper, so it must be the final 201 and never the 103.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := i.daprd.Metrics(c, ctx)
		assert.Equal(c, 1, int(metrics.SumWithLabels("dapr_http_server_response_count", "status:201")))
		assert.Zero(c, metrics.SumWithLabels("dapr_http_server_response_count", "status:103"))
	}, time.Second*10, time.Millisecond*10)
}
