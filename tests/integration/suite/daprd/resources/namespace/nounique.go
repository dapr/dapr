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

package namespace

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
	suite.Register(new(unique))
}

// unique tests that components loaded in self hosted mode with different
// namespaces but same name will cause a component conflict.
type unique struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd

	logline1 *logline.LogLine
	logline2 *logline.LogLine
}

func (u *unique) Setup(t *testing.T) []framework.Option {
	u.logline1 = logline.New(t, logline.WithStdoutLineContains(
		"Fatal error from runtime: failed to load components: duplicate definition of component name 123 (state.in-memory/v1) with existing 123 (state.in-memory/v1)",
	))
	u.daprd1 = daprd.New(t, daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 123
 namespace: foobar
spec:
 type: state.in-memory
 version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 123
 namespace: barfoo
spec:
 type: state.in-memory
 version: v1
`),
		daprd.WithNamespace("mynamespace"),
		daprd.WithExecOptions(
			exec.WithExitCode(1),
			exec.WithRunError(func(t *testing.T, err error) {
				require.ErrorContains(t, err, "exit status 1")
			}),
			exec.WithStdout(u.logline1.Stdout()),
		),
	)

	u.logline2 = logline.New(t, logline.WithStdoutLineContains(
		"Fatal error from runtime: failed to load components: duplicate definition of component name 456 (pubsub.in-memory/v1) with existing 456 (state.in-memory/v1)",
	))
	u.daprd2 = daprd.New(t, daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 456
spec:
  type: state.in-memory
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 456
 namespace: default
spec:
 type: pubsub.in-memory
 version: v1
`),
		daprd.WithExecOptions(
			exec.WithExitCode(1),
			exec.WithRunError(func(t *testing.T, err error) {
				require.ErrorContains(t, err, "exit status 1")
			}),
			exec.WithStdout(u.logline2.Stdout()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(u.logline1, u.logline2, u.daprd1, u.daprd2),
	}
}

func (u *unique) Run(t *testing.T, ctx context.Context) {
	u.logline1.EventuallyFoundAll(t)
	u.logline2.EventuallyFoundAll(t)
}
