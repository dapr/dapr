/*
Copyright 2023 The Dapr Authors
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

package app

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(daprapitoken))
}

// daprapitoken tests Dapr send the correct token to the app.
type daprapitoken struct {
	daprd *daprd.Daprd
	srv   *prochttp.HTTP
	reqCh chan *http.Request
}

func (d *daprapitoken) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {})
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		d.reqCh <- r
	})

	d.reqCh = make(chan *http.Request, 1)
	d.srv = prochttp.New(t,
		prochttp.WithHandler(handler),
	)
	d.daprd = daprd.New(t,
		daprd.WithAppPort(d.srv.Port()),
		daprd.WithExecOptions(exec.WithEnvVars(
			"APP_API_TOKEN", "mytoken",
		)),
	)

	return []framework.Option{
		framework.WithProcesses(d.srv, d.daprd),
	}
}

func (d *daprapitoken) Run(t *testing.T, ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, time.Second*3)
	t.Cleanup(cancel)
	d.daprd.WaitUntilRunning(t, ctx)

	url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/foo", d.daprd.HTTPPort(), d.daprd.AppID())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	_, err = http.DefaultClient.Do(req)
	require.NoError(t, err)

	select {
	case <-ctx.Done():
		assert.Fail(t, "timeout waiting for request")
	case r := <-d.reqCh:
		assert.Equal(t, "mytoken", r.Header.Get("Dapr-Api-Token"))
	}
}
