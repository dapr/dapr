/*
Copyright 2026 The Dapr Authors
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

package scheduler

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(bindfail))
}

// bindfail starts a scheduler whose API port is already held. The scheduler
// must exit so the next process gets new sockets: retrying the bind inside
// the same process fails for as long as the holder lives, and a replica whose
// port is held by one of its own outbound connections stays unready until
// its liveness probe restarts it.
type bindfail struct {
	scheduler *scheduler.Scheduler
	logline   *logline.LogLine
}

func (b *bindfail) Setup(t *testing.T) []framework.Option {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Close() })

	b.logline = logline.New(t, logline.WithStdoutLineContains(
		"Fatal error running scheduler",
		"could not listen on port",
	))
	b.scheduler = scheduler.New(t,
		scheduler.WithPort(held.Addr().(*net.TCPAddr).Port),
		scheduler.WithExecOptions(
			exec.WithExitCode(1),
			exec.WithRunError(func(t *testing.T, err error) {
				require.ErrorContains(t, err, "exit status 1")
			}),
			exec.WithStdout(b.logline.Stdout()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(b.logline, b.scheduler),
	}
}

func (b *bindfail) Run(t *testing.T, ctx context.Context) {
	b.logline.EventuallyFoundAll(t)
}
