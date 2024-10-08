/*
Copyright 2024 The Dapr Authors
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

package resources

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
	suite.Register(new(workflow))
}

// workflow ensures a Component with type `dapr.workflow` cannot be loaded.
type workflow struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
}

func (w *workflow) Setup(t *testing.T) []framework.Option {
	w.logline = logline.New(t,
		logline.WithStdoutLineContains(
			"process component myworkflow error: incorrect type workflow.dapr",
		),
	)

	w.daprd = daprd.New(t, daprd.WithResourceFiles(
		`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: myworkflow
spec:
  type: workflow.dapr
  version: v1
`),
		daprd.WithExecOptions(
			exec.WithExitCode(1),
			exec.WithRunError(func(t *testing.T, err error) {
				require.ErrorContains(t, err, "exit status 1")
			}),
			exec.WithStdout(w.logline.Stdout()),
		),
	)
	return []framework.Option{
		framework.WithProcesses(w.logline, w.daprd),
	}
}

func (w *workflow) Run(t *testing.T, ctx context.Context) {
	w.logline.EventuallyFoundAll(t)
}
