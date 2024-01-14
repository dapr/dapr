/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package grpc

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/dapr/components-contrib/state"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(ttl))
}

type ttl struct {
	daprd *daprd.Daprd
	place *placement.Placement
}

func (l *ttl) Setup(t *testing.T) []framework.Option {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: actorstatettl
spec:
 features:
 - name: ActorStateTTL
   enabled: true
`), 0o600))

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`OK`))
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	l.place = placement.New(t)
	l.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithConfigs(configFile),
		daprd.WithPlacementAddresses(l.place.Address()),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(l.place, srv, l.daprd),
	}
}

func (l *ttl) Run(t *testing.T, ctx context.Context) {
	l.place.WaitUntilRunning(t, ctx)
	l.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.DialContext(ctx, l.daprd.GRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err = client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		//nolint:testifylint
		assert.NoError(c, err)
	}, time.Second*20, time.Millisecond*100, "actor not ready")

	now := time.Now()

	_, err = client.ExecuteActorStateTransaction(ctx, &rtv1.ExecuteActorStateTransactionRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Operations: []*rtv1.TransactionalActorStateOperation{
			{
				OperationType: string(state.OperationUpsert),
				Key:           "mykey",
				Value:         &anypb.Any{Value: []byte("myvalue")},
				Metadata: map[string]string{
					"ttlInSeconds": "2",
				},
			},
		},
	})
	require.NoError(t, err)

	t.Run("ensure the state key returns a ttlExpireTime", func(t *testing.T) {
		var resp *rtv1.GetActorStateResponse
		resp, err = client.GetActorState(ctx, &rtv1.GetActorStateRequest{
			ActorType: "myactortype", ActorId: "myactorid", Key: "mykey",
		})
		require.NoError(t, err)

		assert.Equal(t, "myvalue", string(resp.GetData()))
		ttlExpireTimeStr, ok := resp.GetMetadata()["ttlExpireTime"]
		require.True(t, ok)
		var ttlExpireTime time.Time
		ttlExpireTime, err = time.Parse(time.RFC3339, ttlExpireTimeStr)
		require.NoError(t, err)
		assert.InDelta(t, now.Add(2*time.Second).Unix(), ttlExpireTime.Unix(), 1)
	})

	t.Run("can update ttl with new value", func(t *testing.T) {
		_, err = client.ExecuteActorStateTransaction(ctx, &rtv1.ExecuteActorStateTransactionRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Operations: []*rtv1.TransactionalActorStateOperation{
				{
					OperationType: string(state.OperationUpsert),
					Key:           "mykey",
					Value:         &anypb.Any{Value: []byte("myvalue")},
					Metadata: map[string]string{
						"ttlInSeconds": "3",
					},
				},
			},
		})
		require.NoError(t, err)

		time.Sleep(time.Second * 1)

		var resp *rtv1.GetActorStateResponse
		resp, err = client.GetActorState(ctx, &rtv1.GetActorStateRequest{
			ActorType: "myactortype", ActorId: "myactorid", Key: "mykey",
		})
		require.NoError(t, err)

		assert.Equal(t, "myvalue", string(resp.GetData()))
	})

	t.Run("ensure the state key is deleted after the ttl", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetActorState(ctx, &rtv1.GetActorStateRequest{
				ActorType: "myactortype",
				ActorId:   "myactorid",
				Key:       "mykey",
			})
			require.NoError(c, err)
			assert.Empty(c, resp.GetData())
			assert.Empty(c, resp.GetMetadata())
		}, 5*time.Second, 100*time.Millisecond)
	})
}
