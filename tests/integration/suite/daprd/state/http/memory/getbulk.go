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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(getbulk))
}

type getbulk struct {
	daprd *daprd.Daprd
}

func (g *getbulk) Setup(t *testing.T) []framework.Option {
	if os.Getenv("GITHUB_ACTIONS") == "true" &&
		(runtime.GOOS == "windows" || runtime.GOOS == "darwin") {
		t.Skip("Skipping memory test on Windows and MacOS in GitHub Actions")
	}

	g.daprd = daprd.New(t, daprd.WithInMemoryStateStore("mystore"))

	return []framework.Option{
		framework.WithProcesses(g.daprd),
	}
}

func (g *getbulk) Run(t *testing.T, ctx context.Context) {
	g.daprd.WaitUntilRunning(t, ctx)

	baseMemory := g.daprd.MetricResidentMemoryMi(t, ctx)

	keys := make([]string, 10000)
	for i := range 10000 {
		keys[i] = fmt.Sprintf(`"key%d"`, i)
	}
	input := fmt.Sprintf(`{"keys": [%s]}`, strings.Join(keys, `, `))

	for range 400 {
		g.daprd.HTTPPost2xx(t, ctx, "/v1.0/state/mystore/bulk", strings.NewReader(input))
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		memory := g.daprd.MetricResidentMemoryMi(t, ctx)
		assert.InDelta(c, baseMemory, memory, 50)
	}, 25*time.Second, 10*time.Millisecond)
}
