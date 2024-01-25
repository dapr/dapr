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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(dapr))
}

// dapr ensures that a component with the name `dapr` can be loaded.
type dapr struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
}

func (d *dapr) Setup(t *testing.T) []framework.Option {
	d.logline = logline.New(t,
		logline.WithStdoutLineContains(
			"Component loaded: dapr (state.in-memory/v1)",
			"Workflow engine initialized.",
		),
	)

	d.daprd = daprd.New(t, daprd.WithResourceFiles(
		`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: dapr
spec:
  type: state.in-memory
  version: v1
`),
		daprd.WithExecOptions(
			exec.WithStdout(d.logline.Stdout()),
		),
	)
	return []framework.Option{
		framework.WithProcesses(d.logline, d.daprd),
	}
}

func (d *dapr) Run(t *testing.T, ctx context.Context) {
	d.logline.EventuallyFoundAll(t)
}
