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

package activitydispatch

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
	suite.Register(new(validate))
}

// validate asserts that pull dispatch without a per-sidecar activity cap is
// rejected at startup, since the cap is the slot count pull relies on.
type validate struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
}

func (v *validate) Setup(t *testing.T) []framework.Option {
	v.logline = logline.New(t,
		logline.WithStdoutLineContains(
			"workflow activity dispatchMode pull requires maxConcurrentActivityInvocations",
		),
	)

	v.daprd = daprd.New(t,
		daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: pullnoslots
spec:
  workflow:
    activityDispatchMode: pull
`),
		daprd.WithExecOptions(
			exec.WithExitCode(1),
			exec.WithRunError(func(t *testing.T, err error) {
				require.ErrorContains(t, err, "exit status 1")
			}),
			exec.WithStdout(v.logline.Stdout()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(v.logline, v.daprd),
	}
}

func (v *validate) Run(t *testing.T, ctx context.Context) {
	v.logline.EventuallyFoundAll(t)
}
