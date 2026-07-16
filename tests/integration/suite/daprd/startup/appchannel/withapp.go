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
	"time"

	"github.com/stretchr/testify/assert"

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
	daprd   *daprd.Daprd
	app     *app.App
	logline *logline.LogLine
}

func (s *withApp) Setup(t *testing.T) []framework.Option {
	s.app = app.New(t)

	s.logline = logline.New(t, logline.WithCaptureAll())

	s.daprd = daprd.New(t,
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(s.app.Port()),
		daprd.WithExecOptions(
			exec.WithStdout(s.logline.Stdout()),
			exec.WithStderr(s.logline.Stderr()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(s.app, s.logline, s.daprd),
	}
}

func (s *withApp) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	s.logline.EventuallyContains(t,
		"application discovered on port",
		time.Second*15, time.Millisecond*10,
	)
	assert.False(t, s.logline.Contains("waiting for application to listen on port"))
}
