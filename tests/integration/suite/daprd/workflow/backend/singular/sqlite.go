/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwb.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package singular

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(sqlite))
}

// sqlite ensures that 2 sqlite workflow backends cannot be loaded at the same time.
type sqlite struct {
	logline *logline.LogLine
	daprd   *daprd.Daprd
}

func (s *sqlite) Setup(t *testing.T) []framework.Option {
	s.logline = logline.New(t,
		logline.WithStdoutLineContains(
			"Fatal error from runtime: process component wfbackend2 error: [INIT_COMPONENT_FAILURE]: initialization error occurred for wfbackend2 (workflowbackend.sqlite/v1): cannot create more than one workflow backend component",
		),
	)

	s.daprd = daprd.New(t,
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: wfbackend1
spec:
 type: workflowbackend.sqlite
 version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: wfbackend2
spec:
 type: workflowbackend.sqlite
 version: v1
`),
		daprd.WithExecOptions(
			exec.WithExitCode(1),
			exec.WithRunError(func(t *testing.T, err error) {
				require.ErrorContains(t, err, "exit status 1")
			}),
			exec.WithStdout(s.logline.Stdout()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(s.logline, s.daprd),
	}
}

func (s *sqlite) Run(t *testing.T, ctx context.Context) {
	s.logline.EventuallyFoundAll(t)
}
