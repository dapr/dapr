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
	suite.Register(new(wrongPort))
}

type wrongPort struct {
	daprd             *daprd.Daprd
	logLineAppWaiting *logline.LogLine
	logLineActorErr   *logline.LogLine
}

// Setup has an app listening on a different port than specified as the app port;
// therefore, Dapr waits for app channel readiness before initializing actors,
// and actors never initialize.
func (w *wrongPort) Setup(t *testing.T) []framework.Option {
	w.logLineAppWaiting = logline.New(t, logline.WithStdoutLineContains(
		"waiting for application to listen on port",
	))
	w.logLineActorErr = logline.New(t, logline.WithStdoutLineContains(
		"DaprBuiltInActorFoundRetries",
	))

	app := app.New(t,
		app.WithHandlerFunc("/foo", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`OK`))
		}),
	)

	w.daprd = daprd.New(t,
		daprd.WithAppPort(8080),
		daprd.WithInMemoryActorStateStore("myactorstore"),
		daprd.WithExecOptions(exec.WithStdout(w.logLineAppWaiting.Stdout())),
		daprd.WithExecOptions(exec.WithStdout(w.logLineActorErr.Stdout())),
	)

	return []framework.Option{
		framework.WithProcesses(app, w.logLineAppWaiting, w.logLineActorErr, w.daprd),
	}
}

func (w *wrongPort) Run(t *testing.T, ctx context.Context) {
	w.daprd.WaitUntilRunning(t, ctx)

	w.logLineAppWaiting.EventuallyFoundAll(t)
	w.logLineActorErr.EventuallyFoundNone(t)
}
