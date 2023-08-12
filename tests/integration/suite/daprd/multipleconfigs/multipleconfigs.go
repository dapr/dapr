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

package multipleconfigs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(multipleconfigs))
}

// multipleconfigs tests instantiating daprd with multiple configuration files.
type multipleconfigs struct {
	proc *procdaprd.Daprd
}

func (m *multipleconfigs) Setup(t *testing.T) []framework.Option {
	_, goFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to get current file")
	fixtures := filepath.Join(filepath.Dir(goFile), "fixtures")
	m.proc = procdaprd.New(t,
		procdaprd.WithConfigs(
			filepath.Join(fixtures, "feature1.yaml"),
			filepath.Join(fixtures, "feature2.yaml"),
		),
	)
	return []framework.Option{
		framework.WithProcesses(m.proc),
	}
}

func (m *multipleconfigs) Run(t *testing.T, ctx context.Context) {
	m.proc.WaitUntilRunning(t, ctx)

	// There are not many options in the Configuration YAML that we can test in an integration test
	// The simplest one to test is the preview features, since they are exposedd by the /metadata endpoint
	// These are overridde, so we should only have Feature2 enabled here
	t.Run("second config overrides features", func(t *testing.T) {
		features := m.loadFeatures(t, ctx)
		assert.Contains(t, features, "Feature2")
		assert.NotContains(t, features, "Feature1")
	})
}

func (m *multipleconfigs) loadFeatures(t *testing.T, ctx context.Context) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/v1.0/metadata", m.proc.HTTPPort()), nil)
	require.NoError(t, err)

	resp, err := util.HTTPClient(t).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body := struct {
		EnabledFeatures []string `json:"enabledFeatures"`
	}{}
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)

	return body.EnabledFeatures
}
