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
	suite.Register(new(withApp))
}

type withApp struct {
	daprd             *daprd.Daprd
	logLineAppWaiting *logline.LogLine
	logLineActorErr   *logline.LogLine
}

// Setup has an app listening on the selected app port
// to ensure that Dapr waits for app channel readiness before initializing actors;
// therefore, actors should be initialized here.
func (s *withApp) Setup(t *testing.T) []framework.Option {
	s.logLineAppWaiting = logline.New(t, logline.WithStdoutLineContains(
		"This will block until the app is listening on that port.",
	))
	s.logLineActorErr = logline.New(t, logline.WithStdoutLineContains(
		"DaprBuiltInActorFoundRetries",
	))

	app := app.New(t,
		app.WithHandlerFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	s.daprd = daprd.New(t,
		daprd.WithAppPort(8080),
		daprd.WithInMemoryActorStateStore("myactorstore"),
		daprd.WithExecOptions(exec.WithStdout(s.logLineAppWaiting.Stdout())),
		daprd.WithExecOptions(exec.WithStdout(s.logLineActorErr.Stdout())),
	)

	return []framework.Option{
		framework.WithProcesses(app, s.logLineAppWaiting, s.logLineActorErr, s.daprd),
	}
}

func (s *withApp) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	s.logLineAppWaiting.EventuallyFoundAll(t)
	s.logLineActorErr.EventuallyFoundNone(t)
}
