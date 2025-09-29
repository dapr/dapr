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
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(noApp))
}

type noApp struct {
	daprd             *daprd.Daprd
	logLineAppWaiting *logline.LogLine
	logLineActorErr   *logline.LogLine
}

// SetupWithNoApp intentionally has no app listening on the selected app port
// to ensure that Dapr waits for app channel readiness before initializing actors;
// therefore, no actors should be initialized here.
func (n *noApp) Setup(t *testing.T) []framework.Option {
	n.logLineAppWaiting = logline.New(t, logline.WithStdoutLineContains(
		"waiting for application to listen on port",
	))
	n.logLineActorErr = logline.New(t, logline.WithStdoutLineContains(
		"DaprBuiltInActorFoundRetries",
	))

	n.daprd = daprd.New(t,
		daprd.WithAppPort(8080),
		daprd.WithInMemoryActorStateStore("myactorstore"),
		daprd.WithExecOptions(exec.WithStdout(n.logLineAppWaiting.Stdout())),
		daprd.WithExecOptions(exec.WithStdout(n.logLineActorErr.Stdout())),
	)

	return []framework.Option{
		framework.WithProcesses(n.logLineAppWaiting, n.logLineActorErr, n.daprd),
	}
}

func (n *noApp) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)

	n.logLineAppWaiting.EventuallyFoundAll(t)
	n.logLineActorErr.EventuallyFoundNone(t)
}
