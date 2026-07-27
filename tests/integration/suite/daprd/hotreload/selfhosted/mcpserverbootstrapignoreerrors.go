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
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(mcpserverbootstrapignoreerrors))
}

type mcpserverbootstrapignoreerrors struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
	place   *placement.Placement
	sched   *scheduler.Scheduler
	resDir  string
	mcpPort int
}

func (m *mcpserverbootstrapignoreerrors) Setup(t *testing.T) []framework.Option {
	srv := mcp.NewServer(&mcp.Implementation{Name: "real-mcp", Version: "v1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "echo", Description: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, struct{}{}, nil
	})
	mcpProc := prochttp.New(t, prochttp.WithHandler(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil),
	))
	m.mcpPort = mcpProc.Port()

	m.sched = scheduler.New(t)
	m.place = placement.New(t)

	m.resDir = t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(m.resDir, "a.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: a
spec:
  ignoreErrors: true
  endpoint:
    streamableHTTP:
      url: http://127.0.0.1:1/mcp
`), 0o600))

	m.logline = logline.New(t, logline.WithCaptureAll())

	m.daprd = daprd.New(t,
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithSchedulerAddresses(m.sched.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithResourcesDir(m.resDir),
		daprd.WithLogLevel("debug"),
		daprd.WithLogLineStdout(m.logline),
		daprd.WithExecOptions(exec.WithStderr(m.logline.Stderr())),
	)

	return []framework.Option{
		framework.WithProcesses(m.logline, m.place, m.sched, mcpProc, m.daprd),
	}
}

func (m *mcpserverbootstrapignoreerrors) Run(t *testing.T, ctx context.Context) {
	m.sched.WaitUntilRunning(t, ctx)
	m.place.WaitUntilRunning(t, ctx)
	m.daprd.WaitUntilRunning(t, ctx)

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.Len(t, m.daprd.GetMetaMCPServers(t, ctx), 1)
	}, time.Second*10, time.Millisecond*10)

	t.Run("delete bootstrap-failed MCPServer is processed by reconciler", func(t *testing.T) {
		m.logline.Reset()
		require.NoError(t, os.Remove(filepath.Join(m.resDir, "a.yaml")))
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			assert.Empty(t, m.daprd.GetMetaMCPServers(t, ctx))
		}, time.Second*10, time.Millisecond*10)
	})

	t.Run("add valid MCPServer b should register workflow actors", func(t *testing.T) {
		m.logline.Reset()
		require.NoError(t, os.WriteFile(filepath.Join(m.resDir, "b.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: b
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:`+strconv.Itoa(m.mcpPort)+`
`), 0o600))

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			assert.Len(t, m.daprd.GetMetaMCPServers(t, ctx), 1)
		}, time.Second*10, time.Millisecond*10)

		m.logline.EventuallyContains(t, "Registering workflow actors for internal workflows",
			time.Second*5, time.Millisecond*10)
	})

	t.Run("delete b should unregister workflow actors", func(t *testing.T) {
		m.logline.Reset()
		require.NoError(t, os.Remove(filepath.Join(m.resDir, "b.yaml")))

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			assert.Empty(t, m.daprd.GetMetaMCPServers(t, ctx))
		}, time.Second*10, time.Millisecond*10)

		m.logline.EventuallyContains(t, "Unregistering workflow actors",
			time.Second*5, time.Millisecond*10)
	})

	t.Run("re-add valid MCPServer c should register workflow actors again", func(t *testing.T) {
		m.logline.Reset()
		require.NoError(t, os.WriteFile(filepath.Join(m.resDir, "c.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: c
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:`+strconv.Itoa(m.mcpPort)+`
`), 0o600))

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			assert.Len(t, m.daprd.GetMetaMCPServers(t, ctx), 1)
		}, time.Second*10, time.Millisecond*10)

		m.logline.EventuallyContains(t, "Registering workflow actors for internal workflows",
			time.Second*5, time.Millisecond*10)
	})
}
