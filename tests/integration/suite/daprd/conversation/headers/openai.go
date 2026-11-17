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

package headers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(openai))
}

// openai tests that the OpenAI conversation component sends the HTTP headers
// in its headers metadata field on every request to the model endpoint. The
// field is set as a JSON string, as a YAML object, and from a secret store.
type openai struct {
	srv   *prochttp.HTTP
	daprd *daprd.Daprd

	lock    sync.Mutex
	headers map[string][]http.Header
}

func (o *openai) Setup(t *testing.T) []framework.Option {
	o.headers = make(map[string][]http.Header)

	// Each component has its own endpoint path, so the server can tell the
	// requests apart.
	o.srv = prochttp.New(t, prochttp.WithHandlerFunc("/", func(w http.ResponseWriter, r *http.Request) {
		name, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/chat/completions")
		if !ok {
			http.NotFound(w, r)
			return
		}
		o.lock.Lock()
		o.headers[name] = append(o.headers[name], r.Header.Clone())
		o.lock.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hello from the gateway"},"finish_reason":"stop"}]}`)
	}))

	endpoint := fmt.Sprintf("http://localhost:%d", o.srv.Port())

	o.daprd = daprd.New(t,
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"GATEWAY_HEADERS", `{"X-Tenant-ID":"tenant-secret","X-Gateway-Key":"key-secret"}`,
		)),
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: envstore
spec:
  type: secretstores.local.env
  version: v1
  metadata:
  - name: prefix
    value: GATEWAY_
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: string
spec:
  type: conversation.openai
  version: v1
  metadata:
  - name: key
    value: test-key
  - name: endpoint
    value: %[1]s/string
  - name: headers
    value: '{"X-Tenant-ID":"tenant-string","X-Gateway-Key":"key-string"}'
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: object
spec:
  type: conversation.openai
  version: v1
  metadata:
  - name: key
    value: test-key
  - name: endpoint
    value: %[1]s/object
  - name: headers
    value:
      X-Tenant-ID: tenant-object
      X-Gateway-Key: key-object
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: secret
spec:
  type: conversation.openai
  version: v1
  metadata:
  - name: key
    value: test-key
  - name: endpoint
    value: %[1]s/secret
  - name: headers
    secretKeyRef:
      name: HEADERS
      key: HEADERS
auth:
  secretStore: envstore
`, endpoint)),
	)

	return []framework.Option{
		framework.WithProcesses(o.srv, o.daprd),
	}
}

func (o *openai) Run(t *testing.T, ctx context.Context) {
	o.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	for _, name := range []string{"string", "object", "secret"} {
		t.Run(name, func(t *testing.T) {
			postURL := fmt.Sprintf("http://%s/v1.0-alpha2/conversation/%s/converse", o.daprd.HTTPAddress(), name)
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
				assert.Contains(t, string(body), "hello from the gateway")
			}

			o.lock.Lock()
			got := o.headers[name]
			o.lock.Unlock()
			require.Len(t, got, 2)
			for _, h := range got {
				assert.Equal(t, "tenant-"+name, h.Get("X-Tenant-ID"))
				assert.Equal(t, "key-"+name, h.Get("X-Gateway-Key"))
				assert.Equal(t, "Bearer test-key", h.Get("Authorization"))
			}
		})
	}
}
