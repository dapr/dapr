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

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
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

	app := app.New(t,
		app.WithHandlerFunc("/foo",
			func(http.ResponseWriter, *http.Request) {},
		),
	)

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

	_, err := i.daprd.GRPCClient(t, ctx).InvokeService(ctx, &rtv1.InvokeServiceRequest{
		Id: i.daprd.AppID(),
		Message: &common.InvokeRequest{
			Method:        "foo",
			HttpExtension: &common.HTTPExtension{Verb: common.HTTPExtension_GET},
		},
	})
	require.NoError(t, err)
}
