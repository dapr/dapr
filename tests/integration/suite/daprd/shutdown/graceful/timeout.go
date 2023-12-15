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

package graceful

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(timeout))
}

// timeout tests Daprd's --dapr-graceful-shutdown-seconds where Daprd force exits
// because the graceful timeout expires.
type timeout struct {
	daprd       *daprd.Daprd
	closeInvoke chan struct{}
	inInvoke    chan struct{}
}

func (i *timeout) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on windows which relies on unix process signals")
	}

	i.closeInvoke = make(chan struct{})
	i.inInvoke = make(chan struct{})
	handler := http.NewServeMux()
	handler.HandleFunc("/foo", func(w http.ResponseWriter, r *http.Request) {
		close(i.inInvoke)
		<-i.closeInvoke
	})
	app := prochttp.New(t,
		prochttp.WithHandler(handler),
	)

	logline := logline.New(t,
		logline.WithStdoutLineContains(
			"Graceful shutdown timeout exceeded, forcing shutdown",
		),
	)

	i.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithDaprGracefulShutdownSeconds(1),
		daprd.WithExecOptions(
			exec.WithExitCode(1),
			exec.WithRunError(func(t *testing.T, err error) {
				require.ErrorContains(t, err, "exit status 1")
			}),
			exec.WithStdout(logline.Stdout()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(app, logline),
	}
}

func (i *timeout) Run(t *testing.T, ctx context.Context) {
	i.daprd.Run(t, ctx)
	i.daprd.WaitUntilRunning(t, ctx)
	client := util.HTTPClient(t)

	reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/foo", i.daprd.HTTPPort(), i.daprd.AppID())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	require.NoError(t, err)
	errCh := make(chan error)
	go func() {
		resp, cerr := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		errCh <- cerr
	}()
	<-i.inInvoke
	i.daprd.Cleanup(t)
	close(i.closeInvoke)
	require.ErrorIs(t, <-errCh, io.EOF)
}
