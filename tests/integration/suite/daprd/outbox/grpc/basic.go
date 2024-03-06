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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	daprd *daprd.Daprd
	lock  sync.Mutex
	msg   []byte
}

func (o *basic) Setup(t *testing.T) []framework.Option {
	onTopicEvent := func(ctx context.Context, in *runtimev1pb.TopicEventRequest) (*runtimev1pb.TopicEventResponse, error) {
		o.lock.Lock()
		defer o.lock.Unlock()
		o.msg = in.GetData()
		return &runtimev1pb.TopicEventResponse{
			Status: runtimev1pb.TopicEventResponse_SUCCESS,
		}, nil
	}

	srv1 := app.New(t, app.WithOnTopicEventFn(onTopicEvent))
	o.daprd = daprd.New(t, daprd.WithAppID("outboxtest"), daprd.WithAppPort(srv1.Port(t)), daprd.WithAppProtocol("grpc"), daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.in-memory
  version: v1
  metadata:
  - name: outboxPublishPubsub
    value: "mypubsub"
  - name: outboxPublishTopic
    value: "test"
`,
		`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'mypubsub'
spec:
  type: pubsub.in-memory
  version: v1
`,
		`
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
  name: 'order'
spec:
  topic: 'test'
  routes:
    default: '/test'
  pubsubname: 'mypubsub'
scopes:
- outboxtest
`))

	return []framework.Option{
		framework.WithProcesses(srv1, o.daprd),
	}
}

func (o *basic) Run(t *testing.T, ctx context.Context) {
	o.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.DialContext(ctx, o.daprd.GRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	_, err = runtimev1pb.NewDaprClient(conn).ExecuteStateTransaction(ctx, &runtimev1pb.ExecuteStateTransactionRequest{
		StoreName: "mystore",
		Operations: []*runtimev1pb.TransactionalStateOperation{
			{
				OperationType: "upsert",
				Request: &common.StateItem{
					Key:   "1",
					Value: []byte("2"),
				},
			},
		},
	})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		o.lock.Lock()
		defer o.lock.Unlock()
		return string(o.msg) == "2"
	}, time.Second*5, time.Millisecond*100, "failed to receive message in time")
}
