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

package output

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(memory))
}

type memory struct {
	daprd *daprd.Daprd
}

func (m *memory) Setup(t *testing.T) []framework.Option {
	if os.Getenv("GITHUB_ACTIONS") == "true" &&
		(runtime.GOOS == "windows" || runtime.GOOS == "darwin") {
		t.Skip("Skipping memory test on Windows and MacOS in GitHub Actions")
	}

	app := app.New(t,
		app.WithHandlerFunc("/foo", func(w http.ResponseWriter, r *http.Request) {}),
	)

	m.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithResourceFiles(fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mybin
spec:
  type: bindings.http
  version: v1
  metadata:
  - name: url
    value: http://127.0.0.1:%d/foo
`, app.Port())))

	return []framework.Option{
		framework.WithProcesses(app, m.daprd),
	}
}

func (m *memory) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	quantity, err := resource.ParseQuantity("3Mi")
	require.NoError(t, err)
	bytesN, ok := quantity.AsInt64()
	require.True(t, ok)
	input := `[{"key":"123","value":"` + strings.Repeat("0", int(bytesN)) + `"}]`

	baseMemory := m.daprd.MetricResidentMemoryMi(t, ctx)

	for range 300 {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			m.daprd.HTTPPost2xx(c, ctx, "/v1.0/bindings/mybin", strings.NewReader(`{"operation":"get","data":`+input+`}`))
		}, 10*time.Second, 10*time.Millisecond)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		memory := m.daprd.MetricResidentMemoryMi(t, ctx)
		assert.InDelta(c, baseMemory, memory, 50)
	}, 25*time.Second, 10*time.Millisecond)
}
