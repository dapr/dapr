/*
Copyright 2021 The Dapr Authors
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

package table

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(vNodesAPILevel10))
}

type vNodesAPILevel10 struct {
	place *placement.Placement
}

func (v *vNodesAPILevel10) Setup(t *testing.T) []framework.Option {
	v.place = placement.New(t,
		placement.WithLogLevel("debug"),
		placement.WithMetadataEnabled(true),
	)

	return []framework.Option{
		framework.WithProcesses(v.place),
	}
}

func (v *vNodesAPILevel10) Run(t *testing.T, ctx context.Context) {
	v.place.WaitUntilRunning(t, ctx)

	// Connect
	conn, err := v.place.EstablishConn(ctx)
	require.NoError(t, err)

	// Collect messages
	placementMessageCh := make(chan any)

	// Register the host, with API level 10 (pre v1.13)
	stopCh := make(chan struct{})
	msg := &placementv1pb.Host{
		Name:     "myapp",
		Port:     1234,
		Entities: []string{"someactor"},
		Id:       "myapp1",
		ApiLevel: uint32(10),
	}
	placement.RegisterHost(t, ctx, conn, msg, placementMessageCh, stopCh)
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		select {
		case <-ctx.Done():
			return
		case msgAny := <-placementMessageCh:
			if ctx.Err() != nil {
				return
			}
			switch msg := msgAny.(type) {
			case error:
				assert.Fail(t, "Received an error in the placement channel: '%v'", msg)
			case *placementv1pb.PlacementTables:
				assert.Equal(t, uint32(10), msg.GetApiLevel())
				assert.Len(t, msg.GetEntries(), 1)
				// Check that the placement service sends the vnodes, because of the older cluster API level
				assert.Len(t, msg.GetEntries()["someactor"].GetHosts(), int(msg.GetReplicationFactor()))
				assert.Len(t, msg.GetEntries()["someactor"].GetSortedSet(), int(msg.GetReplicationFactor()))
			}
		}
	}, 10*time.Second, 100*time.Millisecond)
}
