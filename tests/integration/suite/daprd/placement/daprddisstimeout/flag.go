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

package daprddisstimeout

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	dactors "github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(flag))
}

// flag verifies that --actors-disseminate-timeout makes daprd reset its
// placement stream when a dissemination round runs longer than the
// configured value.
type flag struct {
	actors  *dactors.Actors
	place   *placement.Placement
	logline *logline.LogLine
}

func (f *flag) Setup(t *testing.T) []framework.Option {
	// Placement-side timeout intentionally larger than daprd-side, so the
	// daprd reset fires first.
	f.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*15),
	)

	f.logline = logline.New(t, logline.WithStdoutLineContains(
		"Dissemination timeout for version",
	))

	f.actors = dactors.New(t,
		dactors.WithActorTypes("myactor"),
		dactors.WithPlacement(f.place),
		dactors.WithActorTypeHandler("myactor", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`OK`))
		}),
		dactors.WithDaprdOptions(
			daprd.WithActorsDisseminateTimeout(time.Second*2),
			daprd.WithLogLineStdout(f.logline),
		),
	)

	return []framework.Option{
		framework.WithProcesses(f.logline, f.actors),
	}
}

func (f *flag) Run(t *testing.T, ctx context.Context) {
	f.actors.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := f.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.Len(c, table.Tables["default"].Hosts, 1)
	}, time.Second*15, time.Millisecond*100)

	// Connect a host that never ACKs further messages, holding the
	// dissemination round open. With placement-side --disseminate-timeout
	// at 15s and daprd-side --actors-disseminate-timeout at 2s, the daprd
	// side will fire its reset first.
	client := f.place.Client(t, ctx)
	blocker, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, blocker.Send(&v1pb.Host{
		Name: "blocker", Port: 9999,
		Entities: []string{"myactor"}, Id: "blocker", Namespace: "default",
	}))
	go func() {
		for {
			if _, recvErr := blocker.Recv(); recvErr != nil {
				return
			}
		}
	}()

	// Daprd should log its dissemination timeout reset within ~3s of the
	// blocker connecting.
	f.logline.EventuallyFoundAll(t)
}
