package table

import (
	"context"
	"log"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
	placementtests "github.com/dapr/dapr/tests/integration/suite/placement/shared"
)

func init() {
	suite.Register(new(vNodes))
}

type vNodes struct {
	place *placement.Placement
}

func (v *vNodes) Setup(t *testing.T) []framework.Option {
	v.place = placement.New(t,
		placement.WithLogLevel("debug"),
		placement.WithMetadataEnabled(true),
	)

	return []framework.Option{
		framework.WithProcesses(v.place),
	}
}

func (v *vNodes) Run(t *testing.T, ctx context.Context) {
	const (
		level1 = 10
		level2 = 20
	)

	v.place.WaitUntilRunning(t, ctx)

	// Connect
	conn1, err := placementtests.EstablishConn(ctx, v.place.Port())
	require.NoError(t, err)
	conn2, err := placementtests.EstablishConn(ctx, v.place.Port())
	require.NoError(t, err)

	// Collect messages
	placementMessageCh := make(chan any)

	// Register the first host, with a lower API level (pre v1.13)
	stopCh1 := make(chan struct{})
	placementtests.RegisterHost(t, ctx, conn1, "myapp1", level1, placementMessageCh, stopCh1)
	// Check that the placement service sends the vnodes, because of the older cluster API level
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
				log.Printf("\n\n\nReceived an error in the channel: '%v'", msg)
				return
			case *placementv1pb.PlacementTables:
				assert.Equal(t, uint32(level1), msg.GetApiLevel())
				assert.Len(t, msg.GetEntries(), 1)
				assert.Len(t, msg.GetEntries()["someactor"].GetHosts(), int(msg.GetReplicationFactor()))
				assert.Len(t, msg.GetEntries()["someactor"].GetSortedSet(), int(msg.GetReplicationFactor()))
			}
		}
	}, 10*time.Second, 100*time.Millisecond)

	// Register the second host, with a higher API level (v1.13+)
	stopCh2 := make(chan struct{})
	placementtests.RegisterHost(t, ctx, conn2, "myapp2", level2, placementMessageCh, stopCh2)
	// Check that the placement service still sends the vnodes, because cluster level is still pre v1.13
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
				log.Printf("\n\n\nReceived an error in the channel: '%v'", msg)
				return
			case *placementv1pb.PlacementTables:
				assert.Equal(t, uint32(level1), msg.GetApiLevel())
				assert.Len(t, msg.GetEntries(), 1)
				assert.Len(t, msg.GetEntries()["someactor"].GetHosts(), int(msg.GetReplicationFactor()))
				assert.Len(t, msg.GetEntries()["someactor"].GetSortedSet(), int(msg.GetReplicationFactor()))
			}
		}
	}, 10*time.Second, 100*time.Millisecond)

	// Stop the first host, and the in API level should increase and vnodes shouldn't b sent anymore
	close(stopCh1)
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
				log.Printf("\n\n\nReceived an error in the channel: '%v'", msg)
				return
			case *placementv1pb.PlacementTables:
				assert.Equal(t, uint32(level2), msg.GetApiLevel())
				assert.Len(t, msg.GetEntries(), 1)
				assert.Empty(t, msg.GetEntries()["someactor"].GetHosts())
				assert.Empty(t, msg.GetEntries()["someactor"].GetSortedSet())
			}
		}
	}, 10*time.Second, 100*time.Millisecond)
}
