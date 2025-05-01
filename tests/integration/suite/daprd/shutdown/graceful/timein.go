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
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(timein))
}

// timein tests Daprd's --dapr-graceful-shutdown-seconds gracefully
// terminates on all resources closing.
type timein struct {
	daprd *daprd.Daprd
}

func (i *timein) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	app := prochttp.New(t, prochttp.WithHandler(http.NewServeMux()))

	logline := logline.New(t,
		logline.WithStdoutLineContains(
			"Daprd shutdown gracefully",
		),
	)

	i.daprd = daprd.New(t,
		daprd.WithDaprGracefulShutdownSeconds(5),
		daprd.WithAppPort(app.Port()),
		daprd.WithExecOptions(exec.WithStdout(logline.Stdout())),
	)
	return []framework.Option{
		framework.WithProcesses(logline, app, i.daprd),
	}
}

func (i *timein) Run(t *testing.T, ctx context.Context) {
	i.daprd.WaitUntilRunning(t, ctx)
}
