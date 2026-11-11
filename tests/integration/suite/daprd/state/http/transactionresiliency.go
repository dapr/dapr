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
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(transactionresiliency))
}

// transactionresiliency ensures that the resiliency policy timeout of a
// state store reaches the store when a transaction is sent over HTTP.
type transactionresiliency struct {
	daprd *procdaprd.Daprd
	store *fault.Store
}

func (r *transactionresiliency) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("skipping unix socket based test on windows")
	}

	r.store = fault.New(t)

	sock := socket.New(t)
	ss := statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(r.store),
	)

	r.daprd = procdaprd.New(t,
		procdaprd.WithSocket(t, sock),
		procdaprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: myresiliency
spec:
  policies:
    timeouts:
      short: 1s
  targets:
    components:
      mystore:
        outbound:
          timeout: short
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.%s
  version: v1
`, ss.SocketName())),
	)

	return []framework.Option{
		framework.WithProcesses(ss, r.daprd),
	}
}

func (r *transactionresiliency) Run(t *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(t, ctx)

	arrived, release := r.store.ArmMultiHold("txkey")
	t.Cleanup(release)

	url := fmt.Sprintf("http://localhost:%d/v1.0/state/mystore/transaction", r.daprd.HTTPPort())
	body := `{"operations":[{"operation":"upsert","request":{"key":"txkey","value":"v"}}]}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.HTTP(t).Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	select {
	case <-arrived:
	default:
		assert.Fail(t, "the transaction never reached the store")
	}

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	remaining, ok := r.store.MultiDeadline()
	require.True(t, ok, "the store context has no deadline")
	assert.Positive(t, remaining)
	assert.LessOrEqual(t, remaining, time.Second)
}
