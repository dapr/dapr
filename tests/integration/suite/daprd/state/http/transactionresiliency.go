/*
Copyright 2026 The Dapr Authors
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

package http

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/inmemory"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(transactionResiliency))
}

// transactionResiliency verifies that the HTTP state transaction API calls
// the state store with the context of the resiliency policy, so the policy
// timeout reaches the store.
type transactionResiliency struct {
	daprd    *procdaprd.Daprd
	deadline chan time.Duration
}

type deadlineStore struct {
	*inmemory.Wrapped
	deadline chan time.Duration
}

func (d *deadlineStore) Multi(ctx context.Context, req *state.TransactionalStateRequest) error {
	var remaining time.Duration
	if dl, ok := ctx.Deadline(); ok {
		remaining = time.Until(dl)
	}
	d.deadline <- remaining
	return d.Wrapped.Store.(state.TransactionalStore).Multi(ctx, req)
}

func (d *deadlineStore) MultiMaxSize() int {
	return -1
}

func (tr *transactionResiliency) Setup(t *testing.T) []framework.Option {
	tr.deadline = make(chan time.Duration, 1)

	sock := socket.New(t)
	store := statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(&deadlineStore{
			Wrapped:  inmemory.New(t).(*inmemory.Wrapped),
			deadline: tr.deadline,
		}),
	)

	tr.daprd = procdaprd.New(t,
		procdaprd.WithSocket(t, sock),
		procdaprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.%s
  version: v1
`, store.SocketName()), `
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: myresiliency
spec:
  policies:
    timeouts:
      storeTimeout: 30s
  targets:
    components:
      mystore:
        outbound:
          timeout: storeTimeout
`),
	)

	return []framework.Option{
		framework.WithProcesses(store, tr.daprd),
	}
}

func (tr *transactionResiliency) Run(t *testing.T, ctx context.Context) {
	tr.daprd.WaitUntilRunning(t, ctx)

	url := fmt.Sprintf("http://%s/v1.0/state/mystore/transaction", tr.daprd.HTTPAddress())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(
		`{"operations":[{"operation":"upsert","request":{"key":"key1","value":"value1"}}]}`,
	))
	require.NoError(t, err)
	resp, err := client.HTTP(t).Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	select {
	case remaining := <-tr.deadline:
		assert.Positive(t, remaining, "state store was not called with the resiliency policy timeout")
		assert.LessOrEqual(t, remaining, 30*time.Second)
	case <-ctx.Done():
		require.Fail(t, "state store Multi was not called")
	}
}
