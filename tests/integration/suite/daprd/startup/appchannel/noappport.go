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
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(noAppPort))
}

type noAppPort struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
}

func (n *noAppPort) Setup(t *testing.T) []framework.Option {
	n.logline = logline.New(t, logline.WithCaptureAll())

	n.daprd = daprd.New(t,
		daprd.WithExecOptions(
			exec.WithStdout(n.logline.Stdout()),
			exec.WithStderr(n.logline.Stderr()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(n.logline, n.daprd),
	}
}

func (n *noAppPort) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)

	assert.Never(t, func() bool {
		return n.logline.Contains("This will block until the app is listening on that port.") ||
			n.logline.Contains("waiting for application to listen on port")
	}, time.Second*3, time.Millisecond*10)
}
