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

package output

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/dapr/dapr/pkg/proto/common/v1"
	runtime "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	suite.Register(new(bindingtraceparent))
}

type bindingtraceparent struct {
	// add grpc app
	srv   *prochttp.HTTP
	daprd *daprd.Daprd
}

func (b *bindingtraceparent) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		// print headers
		fmt.Printf("\noutput headers: %+v\n", r.Header)
		w.Write([]byte(`OK`))
	})

	b.srv = prochttp.New(t, prochttp.WithHandler(handler))
	b.daprd = daprd.New(t,
		daprd.WithAppPort(b.srv.Port()),
		daprd.WithResourceFiles(fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: http-binding-traceparent
spec:
  type: bindings.http
  version: v1
  metadata:
  - name: url
    value: http://127.0.0.1:%d/test
`, b.srv.Port())))

	return []framework.Option{
		framework.WithProcesses(b.srv, b.daprd),
	}
}

func (b *bindingtraceparent) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	client := b.daprd.GRPCClient(t, ctx)

	// only for http server
	//assert.Eventually(t, func() bool {
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0/bindings/http-binding-traceparent", b.daprd.HTTPPort())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader("{\"operation\":\"get\"}"))
	require.NoError(t, err)
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, resp.StatusCode, http.StatusOK)

	// invoke app
	// TODO: invoke both apps
	appURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/test", b.daprd.HTTPPort(), b.daprd.AppID())
	appreq, err := http.NewRequestWithContext(ctx, http.MethodPost, appURL, strings.NewReader("{\"operation\":\"get\"}"))
	require.NoError(t, err)
	appresp, err := httpClient.Do(appreq)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, appresp.StatusCode, http.StatusOK)

	//return resp.StatusCode == http.StatusOK
	//}, time.Second*5, 10*time.Millisecond)
	//
	//assert.Eventually(t, func() bool {

	invokereq := runtime.InvokeBindingRequest{
		Name:      "http-binding-traceparent",
		Operation: "get",
	}
	//var header grpcMetadata.MD
	//header["traceparent"] = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	//header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	invokeresp, err := client.InvokeBinding(ctx, &invokereq)
	//fmt.Printf("CASSIE: resp: %+v\n\n", resp)
	//fmt.Printf("CASSIE: header: %+v\n\n", header)
	require.NoError(t, err)
	require.NotNil(t, invokeresp)

	svcreq := runtime.InvokeServiceRequest{
		Id: b.daprd.AppID(),
		Message: &common.InvokeRequest{
			Method:      "test",
			Data:        nil,
			ContentType: "",
			HttpExtension: &common.HTTPExtension{
				Verb:        common.HTTPExtension_GET,
				Querystring: "",
			},
		},
	}

	//invoke app via grpc
	svcresp, err := client.InvokeService(ctx, &svcreq)
	require.NoError(t, err)
	require.NotNil(t, svcresp)

}
