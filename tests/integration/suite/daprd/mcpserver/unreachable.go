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

package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(listToolsUnreachable))
}

// listToolsUnreachable verifies that the dapr.internal.mcp.<name>.ListTools workflow
// fails (non-nil failure state) when the MCP server is unreachable.
type listToolsUnreachable struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client
}

func (s *listToolsUnreachable) Setup(t *testing.T) []framework.Option {
	// Reserve a port that never accepts connections.
	// The port stays occupied so no other process can claim it,
	// but the MCP HTTP request receives no response and hits the timeout.
	reserved := ports.Reserve(t, 1)
	deadPort := reserved.Port(t)

	appProc := app.New(t)

	s.sched = scheduler.New(t)
	s.place = placement.New(t)
	s.daprd = daprd.New(t,
		daprd.WithAppPort(appProc.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: dead-server
spec:
  # Eager tool discovery will fail because the endpoint is unreachable;
  # ignoreErrors=true keeps daprd running so we can assert the workflow
  # was not registered.
  ignoreErrors: true
  endpoint:
    streamableHTTP:
      url: http://localhost:%d/mcp
      timeout: 5s
`, deadPort)),
	)

	return []framework.Option{
		framework.WithProcesses(s.place, s.sched, appProc, s.daprd),
	}
}

func (s *listToolsUnreachable) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)

	t.Run("daprd survives when MCP server is unreachable", func(t *testing.T) {
		// `WaitUntilRunning` (above) already proves daprd didn't crash —
		// `ignoreErrors: true` swallowed the registration failure. Confirm
		// the resource is still in compStore (the metadata API surface)
		// even though its workflows weren't registered.
		servers := s.daprd.GetMetaMCPServers(t, ctx)
		names := make([]string, 0, len(servers))
		for _, m := range servers {
			names = append(names, m.GetName())
		}
		assert.Contains(t, names, "dead-server",
			"unreachable MCPServer should still appear in metadata when ignoreErrors=true")
	})
}
