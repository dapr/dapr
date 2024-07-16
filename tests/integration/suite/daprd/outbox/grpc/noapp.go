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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(noapp))
}

type noapp struct {
	daprd *daprd.Daprd
}

func (n *noapp) Setup(t *testing.T) []framework.Option {
	n.daprd = daprd.New(t, daprd.WithResourceFiles(`
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
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'mypubsub'
spec:
  type: pubsub.in-memory
  version: v1
---
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
		framework.WithProcesses(n.daprd),
	}
}

func (n *noapp) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)

	assert.Len(t, n.daprd.GetMetaRegisteredComponents(t, ctx), 2)
	assert.Empty(t, n.daprd.GetMetaSubscriptions(t, ctx))

	client := n.daprd.GRPCClient(t, ctx)
	_, err := client.ExecuteStateTransaction(ctx, &runtimev1pb.ExecuteStateTransactionRequest{
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
}
