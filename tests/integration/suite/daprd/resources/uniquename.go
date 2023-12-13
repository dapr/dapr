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

package resources

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(uniquename))
}

// uniquename tests dapr will error if a component is registered with a name
// that already exists.
type uniquename struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
}

func (u *uniquename) Setup(t *testing.T) []framework.Option {
	filename := filepath.Join(t.TempDir(), "secrets.json")
	require.NoError(t, os.WriteFile(filename, []byte(`{"key1":"bla"}`), 0o600))

	u.logline = logline.New(t,
		logline.WithStdoutLineContains(
			"Fatal error from runtime: process component foo error: [INIT_COMPONENT_FAILURE]: initialization error occurred for foo (state.in-memory/v1): component foo already exists",
		),
	)

	u.daprd = daprd.New(t, daprd.WithResourceFiles(
		fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: foo
spec:
  type: secretstores.local.file
  version: v1
  metadata:
  - name: secretsFile
    value: '%s'
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: foo
spec:
  type: state.in-memory
  version: v1
`, filename)),
		daprd.WithExecOptions(
			exec.WithExitCode(1),
			exec.WithRunError(func(t *testing.T, err error) {
				require.ErrorContains(t, err, "exit status 1")
			}),
			exec.WithStdout(u.logline.Stdout()),
		),
	)
	return []framework.Option{
		framework.WithProcesses(u.logline, u.daprd),
	}
}

func (u *uniquename) Run(t *testing.T, ctx context.Context) {
	assert.Eventually(t, u.logline.FoundAll, time.Second*5, time.Millisecond*100)
}
