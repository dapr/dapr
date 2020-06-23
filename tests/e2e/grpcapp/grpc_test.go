// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package hellodapr_e2e

import (
	"context"
	"os"
	"testing"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/assert"
	grpc_go "google.golang.org/grpc"
)

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
	tr = runner.NewTestRunner("grpcapp", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestGRPCExecuteStateTransaction(t *testing.T) {
	ExecuteStateTransactionTestCases := []*runtimev1pb.TransactionalStateRequest{
		&runtimev1pb.TransactionalStateRequest{"upsert", commonv1pb.StateItem{
			Key: "key1", Value: []byte("1"), 
		}},
		&runtimev1pb.TransactionalStateRequest{"delete", commonv1pb.StateItem{
			Key: "key1", Value: []byte("1"), 
		}},
		&runtimev1pb.TransactionalStateRequest{"upsert", commonv1pb.StateItem{
			Key: "key2", Value: []byte("2"), 
		}},
	}
	
	t.Run("Test GRPC app for executing state transactions", func(t *testing.T) {
		conn, err := grpc_go.Dial(fmt.Sprintf(":%d", appPort))
		assert.Nil(t, err)
		defer conn.Close()

		client := runtimev1pb.NewDaprClient(conn)
		_, err = client.ExecuteStateTransaction(context.Background(), &runtimev1pb.ExecuteStateTransactionRequest{
			storeName: "statestore",
			Requests: ExecuteStateTransactionTestCases
		}
		responseForKey1, _ := client.GetState(context.Background(), &runtimev1pb.GetStateRequest{
			Key: "key1",
		})
		responseForKey2, _ := client.GetState(context.Background(), &runtimev1pb.GetStateRequest{
			Key: "key2",
		})
		
		assert.Equal(responseForKey1.Data, nil)
		assert.Equal(responseForKey2.Data, []byte("2"))
		)
	}
}