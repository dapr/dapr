/*
Copyright 2023 The Dapr Authors
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

package quorum

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	grpcinsecure "google.golang.org/grpc/credentials/insecure"

	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(noMax))
}

// noMax tests placement reports API level with no maximum.
type noMax struct {
	place *placement.Placement
}

func (n *noMax) Setup(t *testing.T) []framework.Option {
	n.place = placement.New(t)

	return []framework.Option{
		framework.WithProcesses(n.place),
	}
}

func (n *noMax) Run(t *testing.T, ctx context.Context) {
	n.place.WaitUntilRunning(t, ctx)

	// Establish a connection
	conn, err := connectPlacement(ctx, n.place.Port())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, conn.Close())
	})

	// Collect messages
	placementMessageCh := make(chan any)
	currentVersion := atomic.Uint32{}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msgAny := <-placementMessageCh:
				switch msg := msgAny.(type) {
				case error:
					log.Printf("Received an error in the channel. This will make the test fail: '%v'", msg)
					return
				case uint32:
					currentVersion.Store(msg)
				}
			}
		}
	}()

	// Register the first host with API level 10
	ctx1, cancel1 := context.WithCancel(ctx)
	defer cancel1()
	registerHost(ctx1, conn, 10, placementMessageCh)

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.Equal(t, uint32(10), currentVersion.Load())
	}, 5*time.Second, 50*time.Millisecond)
}

func connectPlacement(ctx context.Context, port int) (*grpc.ClientConn, error) {
	// Establish a connection with placement
	return grpc.DialContext(ctx, "localhost:"+strconv.Itoa(port),
		grpc.WithBlock(),
		grpc.WithTransportCredentials(grpcinsecure.NewCredentials()),
	)
}

func registerHost(ctx context.Context, conn *grpc.ClientConn, apiLevel int, placementMessage chan any) {
	msg := &placementv1pb.Host{
		Name:     "myapp",
		Port:     1234,
		Entities: []string{"someactor"},
		Id:       "myapp",
		ApiLevel: uint32(apiLevel),
	}

	// Establish a stream and send the initial heartbeat
	// We need to retry here because this will fail until the instance of placement (the only one) acquires leadership
	var placementStream placementv1pb.Placement_ReportDaprStatusClient
	for j := 0; j < 4; j++ {
		client := placementv1pb.NewPlacementClient(conn)
		stream, rErr := client.ReportDaprStatus(ctx)
		if rErr != nil {
			log.Printf("Failed to connect to placement; will retry: %v", rErr)
			// Sleep before retrying
			time.Sleep(time.Second)
			continue
		}

		rErr = stream.Send(msg)
		if rErr != nil {
			log.Printf("Failed to send message; will retry: %v", rErr)
			_ = stream.CloseSend()
			// Sleep before retrying
			time.Sleep(time.Second)
			continue
		}

		// Receive the first message (which can't be an "update" one anyways) to ensure the connection is ready
		_, rErr = stream.Recv()
		if rErr != nil {
			log.Printf("Failed to receive message; will retry: %v", rErr)
			_ = stream.CloseSend()
			// Sleep before retrying
			time.Sleep(time.Second)
			continue
		}

		placementStream = stream
	}

	if placementStream == nil {
		placementMessage <- errors.New("did not connect to placement in time")
		return
	}

	// Send messages every 500ms
	go func() {
		for ctx.Err() == nil {
			placementStream.Send(msg)
			time.Sleep(500 * time.Millisecond)
		}
	}()

	// Collect all API levels
	go func() {
		for {
			in, rerr := placementStream.Recv()
			if rerr != nil {
				if errors.Is(rerr, context.Canceled) || errors.Is(rerr, io.EOF) {
					// Stream ended
					placementMessage <- nil
					return
				}
				placementMessage <- fmt.Errorf("error from placement: %w", rerr)
			}
			if in.GetOperation() == "update" {
				tables := in.GetTables()
				if tables != nil {
					placementMessage <- in.Tables.ApiLevel
				}
			}
		}
	}()
}
