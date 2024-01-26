/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package http

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(missing))
}

// missing ensures that a config ignores a missing middleware component.
type missing struct {
	daprd *daprd.Daprd
}

func (m *missing) Setup(t *testing.T) []framework.Option {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: uppercase
spec:
  httpPipeline:
    handlers:
      - name: uppercase
        type: middleware.http.uppercase
`), 0o600))

	m.daprd = daprd.New(t, daprd.WithConfigs(configFile))

	return []framework.Option{
		framework.WithProcesses(m.daprd),
	}
}

func (m *missing) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)
}
