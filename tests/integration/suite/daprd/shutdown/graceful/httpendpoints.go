/*
Copyright 2025 The Dapr Authors
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

package graceful

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(httpendpoints))
}

type httpendpoints struct {
	daprd *daprd.Daprd

	inInvoke    atomic.Bool
	closeInvoke chan struct{}
}

func (h *httpendpoints) Setup(t *testing.T) []framework.Option {
	h.closeInvoke = make(chan struct{})

	app := app.New(t,
		app.WithHandlerFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {}),
		app.WithHandlerFunc("/foo", func(w http.ResponseWriter, r *http.Request) {
			h.inInvoke.Store(true)
			<-h.closeInvoke
		}),
	)

	h.daprd = daprd.New(t, daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: mywebsite
spec:
  version: v1alpha1
  baseUrl: http://localhost:%d
`, app.Port())))

	return []framework.Option{
		framework.WithProcesses(app),
	}
}

func (h *httpendpoints) Run(t *testing.T, ctx context.Context) {
	h.daprd.Run(t, ctx)
	h.daprd.WaitUntilRunning(t, ctx)
	t.Cleanup(func() { h.daprd.Cleanup(t) })

	client := client.HTTP(t)

	url := fmt.Sprintf("http://%s/v1.0/invoke/mywebsite/method/foo", h.daprd.HTTPAddress())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	require.NoError(t, err)

	errCh := make(chan error)
	respCh := make(chan *http.Response)
	go func() {
		var resp *http.Response
		resp, err = client.Do(req)
		if resp != nil {
			assert.NoError(t, resp.Body.Close())
		}
		errCh <- err
		respCh <- resp
	}()

	require.Eventually(t, h.inInvoke.Load, time.Second*10, time.Millisecond*10)

	go h.daprd.Cleanup(t)

	select {
	case err = <-errCh:
		assert.Fail(t, "unexpected error returned", err)
	case <-time.After(time.Second * 3):
	}

	close(h.closeInvoke)

	select {
	case err = <-errCh:
		require.NoError(t, err)
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout")
	}

	select {
	case resp := <-respCh:
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout")
	}

	resp, err := client.Do(req)
	require.Error(t, err)
	if resp != nil {
		require.NoError(t, resp.Body.Close())
	}
}
