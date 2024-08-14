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

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(noapp))
}

type noapp struct {
	daprd *procdaprd.Daprd
}

func (n *noapp) Setup(t *testing.T) []framework.Option {
	n.daprd = procdaprd.New(t, procdaprd.WithResourceFiles(`
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

	postURL := fmt.Sprintf("http://localhost:%d/v1.0/state/mystore/transaction", n.daprd.HTTPPort())
	stateReq := state.SetRequest{
		Key:      "1",
		Value:    "2",
		Metadata: map[string]string{"outbox.cloudevent.myapp": "myapp1", "data": "a", "id": "b"},
	}
	tr := stateTransactionRequestBody{
		Operations: []stateTransactionRequestBodyOperation{
			{
				Operation: "upsert",
				Request:   stateReq,
			},
		},
	}

	b, err := json.Marshal(&tr)
	require.NoError(t, err)

	httpClient := client.HTTP(t)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(b))
	require.NoError(t, err)
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Empty(t, string(body))
}
