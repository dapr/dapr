// +build e2e

package grpc_e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

import (
	"context"
	"fmt"
	"os"
	"testing"

	pb "github.com/dapr/dapr/pkg/proto/dapr"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/golang/protobuf/ptypes/any"
	grpc "google.golang.org/grpc"
)

const appPort = 50001
const numHealthChecks = 60 // Number of times to check for endpoint health per app.

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	testApps := []kube.AppDescription{
		{
			AppName:        "grpcapp",
			DaprEnabled:    true,
			ImageName:      "e2e-grpcapp",
			Replicas:       1,
			IngressEnabled: true,
		},
	}

	tr = runner.NewTestRunner("grpcapp", testApps, nil)
	os.Exit(tr.Start(m))
}

func TestGRPCApp(t *testing.T) {

	t.Run("Test", func(t *testing.T) {
		conn, err := grpc.Dial(fmt.Sprintf(":%d", appPort), grpc.WithInsecure())
		if err != nil {
			fmt.Println(err)
		}
		defer conn.Close()

		// Create the client
		client := pb.NewDaprClient(conn)

		_, err = client.SaveState(context.Background(), &pb.SaveStateEnvelope{
			StoreName: "statestore",
			Requests: []*pb.StateRequest{
				&pb.StateRequest{
					Key: "myKey1",
					Value: &any.Any{
						Value: []byte("My State"),
					},
				},
			},
		})
		fmt.Println(err)

		_, err = client.PerformTransaction(context.Background(), &pb.MultiStateEnvelope{
			StoreName: "statestore",
			Requests: []*pb.MultiStateRequest{
				{
					OperationType: "upsert",
					Request: &pb.StateRequest{
						Key: "myKey2",
						Value: &any.Any{
							Value: []byte("My State"),
						},
					},
				},
				{
					OperationType: "upsert",
					Request: &pb.StateRequest{
						Key: "myKey3",
						Value: &any.Any{
							Value: []byte("My State 3"),
						},
					},
				},
				{
					OperationType: "delete",
					Request: &pb.StateRequest{
						Key: "myKey2",
					},
				},
			},
		})

		fmt.Println(err)

		res, err := client.GetState(context.Background(), &pb.GetStateEnvelope{
			StoreName: "statestore",
			Key:       "myKey2",
		})
		fmt.Println(res)
	})

}
