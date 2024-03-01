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

package http

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(skew))
}

type skew struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
}

func (s *skew) Setup(t *testing.T) []framework.Option {
	handler := nethttp.NewServeMux()
	handler.HandleFunc("/", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
		w.Write([]byte(r.Method))
	})

	srv := http.New(t, http.WithHandler(handler))
	s.daprd1 = daprd.New(t, daprd.WithAppPort(srv.Port()))
	s.daprd2 = daprd.New(t,
		daprd.WithAppPort(srv.Port()),
		daprd.WithExecOptions(exec.WithVersion(t, "1.13")),
	)

	return []framework.Option{
		framework.WithProcesses(srv, s.daprd1, s.daprd2),
	}
}

func (s *skew) Run(t *testing.T, ctx context.Context) {
	s.daprd1.WaitUntilRunning(t, ctx)
	s.daprd2.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	for _, url := range []string{
		fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/foo", s.daprd1.HTTPPort(), s.daprd2.AppID()),
		fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/foo", s.daprd2.HTTPPort(), s.daprd1.AppID()),
	} {
		req, err := nethttp.NewRequestWithContext(ctx, "GET", url, nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, nethttp.StatusOK, resp.StatusCode)
		require.Equal(t, "GET", string(body))
	}
}
