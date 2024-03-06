/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package output

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	grpcMetadata "google.golang.org/grpc/metadata"

	"github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(bindingerrors))
}

type bindingerrors struct {
	srv   *prochttp.HTTP
	daprd *daprd.Daprd
}

func (b *bindingerrors) Setup(t *testing.T) []framework.Option {
	mux := http.NewServeMux()
	mux.HandleFunc("/error_404", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	b.srv = prochttp.New(t, prochttp.WithHandler(mux))
	b.daprd = daprd.New(t,
		daprd.WithResourceFiles(fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: github-http-binding-404
spec:
  type: bindings.http
  version: v1
  metadata:
  - name: url
    value: http://127.0.0.1:%d/error_404
`, b.srv.Port())))

	return []framework.Option{
		framework.WithProcesses(b.srv, b.daprd),
	}
}

func (b *bindingerrors) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	client := b.daprd.GRPCClient(t, ctx)
	httpClient := util.HTTPClient(t)

	assert.Eventually(t, func() bool {
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/bindings/github-http-binding-404", b.daprd.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader("{\"operation\":\"get\"}"))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		return (resp.StatusCode == http.StatusInternalServerError) && (resp.Header.Get("Metadata.statuscode") == "404")
	}, time.Second*5, 10*time.Millisecond)

	assert.Eventually(t, func() bool {
		req := runtime.InvokeBindingRequest{
			Name:      "github-http-binding-404",
			Operation: "get",
		}
		var header grpcMetadata.MD
		resp, err := client.InvokeBinding(ctx, &req, grpc.Header(&header))
		require.Error(t, err)
		require.Nil(t, resp)
		statusCodeArr := header.Get("metadata.statuscode")
		require.Len(t, statusCodeArr, 1)
		return statusCodeArr[0] == "404"
	}, time.Second*5, 10*time.Millisecond)
}
