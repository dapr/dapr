/*
Copyright 2025 The Dapr Authors
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
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(local))
}

type local struct {
	daprd *daprd.Daprd

	inInvoke    atomic.Bool
	closeInvoke chan struct{}
}

func (l *local) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	l.closeInvoke = make(chan struct{})

	app := app.New(t,
		app.WithHandlerFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {}),
		app.WithHandlerFunc("/foo", func(w http.ResponseWriter, r *http.Request) {
			l.inInvoke.Store(true)
			<-l.closeInvoke
		}),
	)

	l.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithDaprGracefulShutdownSeconds(180),
		daprd.WithAppHealthProbeInterval(1),
		daprd.WithAppHealthProbeThreshold(1),
		daprd.WithAppHealthCheck(true),
	)

	return []framework.Option{
		framework.WithProcesses(app, l.daprd),
	}
}

func (l *local) Run(t *testing.T, ctx context.Context) {
	l.daprd.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/foo", l.daprd.HTTPAddress(), l.daprd.AppID())
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

	require.Eventually(t, l.inInvoke.Load, time.Second*20, time.Millisecond*10)

	go l.daprd.Cleanup(t)

	select {
	case err = <-errCh:
		assert.Fail(t, "unexpected error returned", err)
	case <-time.After(time.Second * 3):
	}

	close(l.closeInvoke)

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
