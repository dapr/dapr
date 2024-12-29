/*
Copyright 2024 The Dapr Authors
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

package memory

import (
	"context"
	"fmt"
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
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(transaction))
}

type transaction struct {
	daprd *daprd.Daprd
}

func (tr *transaction) Setup(t *testing.T) []framework.Option {
	if os.Getenv("GITHUB_ACTIONS") == "true" &&
		(runtime.GOOS == "windows" || runtime.GOOS == "darwin") {
		t.Skip("Skipping memory test on Windows and MacOS in GitHub Actions")
	}

	tr.daprd = daprd.New(t, daprd.WithInMemoryStateStore("mystore"))

	return []framework.Option{
		framework.WithProcesses(tr.daprd),
	}
}

func (tr *transaction) Run(t *testing.T, ctx context.Context) {
	tr.daprd.WaitUntilRunning(t, ctx)

	baseMemory := tr.daprd.MetricResidentMemoryMi(t, ctx)

	quantity, err := resource.ParseQuantity("3Mi")
	require.NoError(t, err)
	bytesN, ok := quantity.AsInt64()
	require.True(t, ok)
	payload := fmt.Sprintf(`{"operations": [{"operation": "upsert","request": {"key": "key1","value": "%s"}}]}`,
		strings.Repeat("0", int(bytesN)),
	)

	for range 400 {
		tr.daprd.HTTPPost2xx(t, ctx, "/v1.0/state/mystore/transaction", strings.NewReader(payload))
	}

	tr.daprd.HTTPDelete2xx(t, ctx, "/v1.0/state/mystore/key1", nil)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		memory := tr.daprd.MetricResidentMemoryMi(t, ctx)
		assert.InDelta(c, baseMemory, memory, 50)
	}, 30*time.Second, 10*time.Millisecond)
}
