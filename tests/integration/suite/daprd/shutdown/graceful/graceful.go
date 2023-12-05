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
	"net/http"
	"runtime"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(graceful))
}

// graceful tests Daprd's --dapr-graceful-shutdown-seconds gracefully
// terminates on all resources closing.
type graceful struct {
	daprd *daprd.Daprd
}

func (g *graceful) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on windows which relies on unix process signals")
	}

	app := prochttp.New(t, prochttp.WithHandler(http.NewServeMux()))

	logline := logline.New(t,
		logline.WithStdoutLineContains(
			"Daprd shutdown gracefully",
		),
	)

	g.daprd = daprd.New(t,
		daprd.WithDaprGracefulShutdownSeconds(5),
		daprd.WithAppPort(app.Port()),
		daprd.WithExecOptions(exec.WithStdout(logline.Stdout())),
	)
	return []framework.Option{
		framework.WithProcesses(app, logline, g.daprd),
	}
}

func (g *graceful) Run(t *testing.T, ctx context.Context) {
	g.daprd.WaitUntilRunning(t, ctx)
}
