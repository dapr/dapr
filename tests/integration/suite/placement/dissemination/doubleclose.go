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

package dissemination

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(doubleclose))
}

type doubleclose struct {
	place   *placement.Placement
	logline *logline.LogLine
}

func (d *doubleclose) Setup(t *testing.T) []framework.Option {
	d.logline = logline.New(t, logline.WithCaptureAll())

	d.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*10),
		placement.WithDisseminateCoalesceWindow(time.Second),
		placement.WithExecOptions(
			exec.WithStdout(d.logline.Stdout()),
			exec.WithStderr(d.logline.Stderr()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(d.logline, d.place),
	}
}

func (d *doubleclose) Run(t *testing.T, ctx context.Context) {
	d.place.WaitUntilRunning(t, ctx)

	assert.Eventually(t, func() bool {
		return d.place.IsLeader(t, ctx)
	}, time.Second*10, time.Millisecond*10)

	client := d.place.Client(t, ctx)

	hostA := &v1pb.Host{
		Name: "a", Port: 1001, Entities: []string{"actorA"},
		Id: "a", Namespace: "default",
	}

	a, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, a.Send(hostA))

	for _, op := range []string{"lock", "update", "unlock"} {
		r, rerr := a.Recv()
		require.NoError(t, rerr)
		require.Equal(t, op, r.GetOperation())
		require.NoError(t, a.Send(hostA))
	}

	bctx, bcancel := context.WithCancel(ctx)
	b, err := client.ReportDaprStatus(bctx)
	require.NoError(t, err)
	require.NoError(t, b.Send(&v1pb.Host{
		Name: "b", Port: 1002, Entities: []string{"actorB"},
		Id: "b", Namespace: "default",
	}))

	d.logline.EventuallyContains(t,
		"Received status report connection from new namespace=default id=b",
		time.Second*5, time.Millisecond*10)
	bcancel()

	sawFinalUpdate := false
	for range 30 {
		r, rerr := a.Recv()
		require.NoError(t, rerr,
			"healthy stream was closed after another connection aborted (dapr/dapr#10323)")

		if r.GetOperation() == "update" {
			entries := r.GetTables().GetEntries()
			_, hasB := entries["actorB"]
			_, hasA := entries["actorA"]
			sawFinalUpdate = hasA && !hasB
		}

		require.NoError(t, a.Send(hostA))

		if sawFinalUpdate && r.GetOperation() == "unlock" {
			break
		}
	}
	require.True(t, sawFinalUpdate,
		"expected a dissemination round whose table contains actorA but not actorB")

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		table := d.place.PlacementTables(t, ctx)
		if !assert.NotNil(col, table.Tables["default"]) {
			return
		}
		if assert.Len(col, table.Tables["default"].Hosts, 1) {
			assert.Equal(col, "a", table.Tables["default"].Hosts[0].Name)
		}
	}, time.Second*10, time.Millisecond*10)
}
