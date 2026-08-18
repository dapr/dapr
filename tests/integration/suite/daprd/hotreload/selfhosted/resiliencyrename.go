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

package selfhosted

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
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(resiliencyrename))
}

type resiliencyrename struct {
	daprd  *daprd.Daprd
	resDir string
}

func (r *resiliencyrename) Setup(t *testing.T) []framework.Option {
	r.resDir = t.TempDir()

	r.daprd = daprd.New(t,
		daprd.WithResourcesDir(r.resDir),
	)

	return []framework.Option{
		framework.WithProcesses(r.daprd),
	}
}

func (r *resiliencyrename) Run(t *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(t, ctx)

	writeResiliency := func(t *testing.T, name string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(r.resDir, "resiliency.yaml"), fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: %s
spec:
  policies:
    timeouts:
      general: 5s
  targets: {}
`, name), 0o600))
	}

	resiliencyNames := func(t assert.TestingT) []string {
		policies := r.daprd.GetMetaResiliencies(t, ctx)
		names := make([]string, 0, len(policies))
		for _, p := range policies {
			names = append(names, p.GetName())
		}
		return names
	}

	writeResiliency(t, "myresiliency")
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, []string{"myresiliency"}, resiliencyNames(c))
	}, 20*time.Second, 10*time.Millisecond)

	writeResiliency(t, "myresiliency-v2")
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, []string{"myresiliency-v2"}, resiliencyNames(c))
	}, 20*time.Second, 10*time.Millisecond)
}
