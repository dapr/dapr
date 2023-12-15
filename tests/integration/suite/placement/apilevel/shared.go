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

package apilevel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcinsecure "google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

func establishConn(ctx context.Context, port int) (*grpc.ClientConn, error) {
	return grpc.DialContext(ctx, "localhost:"+strconv.Itoa(port),
		grpc.WithBlock(),
		grpc.WithTransportCredentials(grpcinsecure.NewCredentials()),
	)
}

func registerHost(ctx context.Context, conn *grpc.ClientConn, name string, apiLevel int, placementMessage chan any, stopCh chan struct{}) {
	msg := &placementv1pb.Host{
		Name:     name,
		Port:     1234,
		Entities: []string{"someactor"},
		Id:       "myapp",
		ApiLevel: uint32(apiLevel),
	}

	// Establish a stream and send the initial heartbeat
	// We need to retry here because this will fail until the instance of placement (the only one) acquires leadership
	var placementStream placementv1pb.Placement_ReportDaprStatusClient
	for j := 0; j < 5; j++ {
		client := placementv1pb.NewPlacementClient(conn)
		stream, rErr := client.ReportDaprStatus(ctx)
		if rErr != nil {
			log.Printf("Failed to connect to placement; will retry: %v", rErr)
			// Sleep before retrying
			time.Sleep(1500 * time.Millisecond)
			continue
		}

		rErr = stream.Send(msg)
		if rErr != nil {
			log.Printf("Failed to send message; will retry: %v", rErr)
			_ = stream.CloseSend()
			// Sleep before retrying
			time.Sleep(1500 * time.Millisecond)
			continue
		}

		// Receive the first message (which can't be an "update" one anyways) to ensure the connection is ready
		_, rErr = stream.Recv()
		if rErr != nil {
			log.Printf("Failed to receive message; will retry: %v", rErr)
			_ = stream.CloseSend()
			// Sleep before retrying
			time.Sleep(1500 * time.Millisecond)
			continue
		}

		placementStream = stream
	}

	if placementStream == nil {
		placementMessage <- errors.New("did not connect to placement in time")
		return
	}

	// Send messages every second
	go func() {
		for {
			select {
			case <-ctx.Done():
				placementStream.CloseSend()
				return
			case <-stopCh:
				// Disconnect when there's a signal on stopCh
				placementStream.CloseSend()
				return
			case <-time.After(time.Second):
				placementStream.Send(msg)
			}
		}
	}()

	// Collect all API levels
	go func() {
		for {
			in, rerr := placementStream.Recv()
			if rerr != nil {
				if ctx.Err() != nil || errors.Is(rerr, context.Canceled) || errors.Is(rerr, io.EOF) || status.Code(rerr) == codes.Canceled {
					// Stream ended
					placementMessage <- nil
					return
				}
				placementMessage <- fmt.Errorf("error from placement: %w", rerr)
				return
			}
			if in.GetOperation() == "update" {
				placementMessage <- in.GetTables().GetApiLevel()
			}
		}
	}()
}

// Expect the registration to fail with FailedPrecondition.
func registerHostFailing(t *testing.T, ctx context.Context, conn *grpc.ClientConn, apiLevel int) {
	msg := &placementv1pb.Host{
		Name:     "myapp-fail",
		Port:     1234,
		Entities: []string{"someactor"},
		Id:       "myapp",
		ApiLevel: uint32(apiLevel),
	}

	client := placementv1pb.NewPlacementClient(conn)
	stream, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err, "failed to establish stream")

	err = stream.Send(msg)
	require.NoError(t, err, "failed to send message")

	// Should fail here
	_, err = stream.Recv()
	require.Error(t, err)
	require.Equalf(t, codes.FailedPrecondition, status.Code(err), "error was: %v", err)
}

// Checks the API level reported in the state tables matched.
func checkAPILevelInState(t require.TestingT, client *http.Client, port int, expectAPILevel int) (tableVersion int) {
	res, err := client.Get(fmt.Sprintf("http://localhost:%d/placement/state", port))
	require.NoError(t, err)
	defer res.Body.Close()

	stateRes := struct {
		APILevel     int `json:"apiLevel"`
		TableVersion int `json:"tableVersion"`
	}{}
	err = json.NewDecoder(res.Body).Decode(&stateRes)
	require.NoError(t, err)

	assert.Equal(t, expectAPILevel, stateRes.APILevel)

	return stateRes.TableVersion
}
