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
	suite.Register(new(defaulttimeout))
}

type defaulttimeout struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
}

func (d *defaulttimeout) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.Method))
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))

	resiliency := `
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: myresiliency
spec:
  policies:
    timeouts:
      DefaultAppTimeoutPolicy: 30s
  targets: {}
`
	d.daprd1 = daprd.New(t,
		daprd.WithAppPort(srv.Port()),
		daprd.WithResourceFiles(resiliency),
		daprd.WithLogLevel("debug"),
	)
	d.daprd2 = daprd.New(t,
		daprd.WithAppPort(srv.Port()),
		daprd.WithResourceFiles(resiliency),
	)

	return []framework.Option{
		framework.WithProcesses(srv, d.daprd1, d.daprd2),
	}
}

func (d *defaulttimeout) Run(t *testing.T, ctx context.Context) {
	d.daprd1.WaitUntilRunning(t, ctx)
	d.daprd2.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/test", d.daprd1.HTTPPort(), d.daprd2.AppID())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "GET", string(body))
}
