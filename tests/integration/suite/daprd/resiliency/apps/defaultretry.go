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

package apps

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(defaultretry))
}

type defaultretry struct {
	daprd1    *daprd.Daprd
	daprd2    *daprd.Daprd
	callCount map[string]*atomic.Int32
}

func (d *defaultretry) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Method))
	})

	handler.HandleFunc("/retry", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		key := string(body)
		if d.callCount[key] == nil {
			d.callCount[key] = &atomic.Int32{}
		}
		d.callCount[key].Add(1)
		w.Header().Set("x-method", r.Method)
		w.Header().Set("content-type", "application/json")
		if key == "success" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
		w.Write(body)
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))

	resiliency := `
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: myresiliency
spec:
  policies:
    retries:
      DefaultAppRetryPolicy:
        policy: constant
        duration: 100ms
        maxRetries: 3
  targets: {}
`
	d.daprd1 = daprd.New(t,
		daprd.WithAppPort(srv.Port()),
		daprd.WithResourceFiles(resiliency),
	)
	d.daprd2 = daprd.New(t,
		daprd.WithAppPort(srv.Port()),
		daprd.WithResourceFiles(resiliency),
	)
	d.callCount = make(map[string]*atomic.Int32)

	return []framework.Option{
		framework.WithProcesses(srv, d.daprd1, d.daprd2),
	}
}

func (d *defaultretry) Run(t *testing.T, ctx context.Context) {
	d.daprd1.WaitUntilRunning(t, ctx)
	d.daprd2.WaitUntilRunning(t, ctx)

	t.Run("no retry on successful statuscode", func(t *testing.T) {
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/retry", d.daprd1.HTTPPort(), d.daprd2.AppID())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader("success"))
		require.NoError(t, err)
		resp, err := util.HTTPClient(t).Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, "POST", resp.Header.Get("x-method"))
		assert.Equal(t, "application/json", resp.Header.Get("content-type"))
		assert.Equal(t, "success", string(body))
		// CallCount["success"] should be 1 as there would be no retry on successful statuscode
		assert.Equal(t, int32(1), d.callCount["success"].Load())
	})

	t.Run("retry on unsuccessful statuscode", func(t *testing.T) {
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/retry", d.daprd1.HTTPPort(), d.daprd2.AppID())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader("fail"))
		require.NoError(t, err)
		resp, err := util.HTTPClient(t).Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, "POST", resp.Header.Get("x-method"))
		assert.Equal(t, "application/json", resp.Header.Get("content-type"))
		assert.Equal(t, "fail", string(body))
		// Callcount["fail"] should be 4 as there will be 3 retries(maxRetries=3)
		assert.Equal(t, int32(4), d.callCount["fail"].Load())
	})
}
