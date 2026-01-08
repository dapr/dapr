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

package workflow

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/tee"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(stateready))
}

type stateready struct {
	workflow *workflow.Workflow
	stdout   io.Reader
}

func (s *stateready) Setup(t *testing.T) []framework.Option {
	stdoutPipeR, stdoutPipeW := io.Pipe()

	s.stdout = tee.Buffer(t, stdoutPipeR).Add(t)
	s.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithExecOptions(exec.WithStdout(stdoutPipeW)),
		),
	)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *stateready) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	s.workflow.Registry().AddOrchestratorN("bar", func(ctx *task.OrchestrationContext) (any, error) {
		return nil, nil
	})

	s.workflow.BackendClient(t, ctx)

	var buf bytes.Buffer
	var lock sync.Mutex
	go func() {
		bb := make([]byte, 1024)
		for {
			n, err := s.stdout.Read(bb)
			if err != nil {
				return
			}
			lock.Lock()
			buf.Write(bb[:n])
			lock.Unlock()
		}
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		lock.Lock()
		defer lock.Unlock()

		assert.Equal(c, 2, strings.Count(
			buf.String(),
			"Skipping migration, no missing scheduler reminders found",
		))
	}, time.Second*20, time.Millisecond*10)
}
