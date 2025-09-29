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

package appchannel

import (
	"context"
	"net/http"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(noAppPort))
}

type noAppPort struct {
	daprd             *daprd.Daprd
	logLineAppWaiting *logline.LogLine
	logLineActorErr   *logline.LogLine
}

// Setup does not specify an app port,
// so Dapr should not wait for app channel readiness and should initialize actors normally.
func (n *noAppPort) Setup(t *testing.T) []framework.Option {
	n.logLineAppWaiting = logline.New(t, logline.WithStdoutLineContains(
		"waiting for application to listen on port",
	))
	n.logLineActorErr = logline.New(t, logline.WithStdoutLineContains(
		"DaprBuiltInActorNotFoundRetries",
	))

	app := app.New(t,
		app.WithHandlerFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	n.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("myactorstore"),
		daprd.WithExecOptions(exec.WithStdout(n.logLineAppWaiting.Stdout())),
		daprd.WithExecOptions(exec.WithStdout(n.logLineActorErr.Stdout())),
	)

	return []framework.Option{
		framework.WithProcesses(app, n.logLineAppWaiting, n.logLineActorErr, n.daprd),
	}
}

func (n *noAppPort) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)

	n.logLineAppWaiting.EventuallyFoundNone(t)
	n.logLineActorErr.EventuallyFoundNone(t)
}
