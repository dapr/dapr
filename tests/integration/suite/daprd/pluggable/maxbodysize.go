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
	"bytes"
	"context"
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/components-contrib/state"
	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/inmemory"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(maxbodysize))
}

type maxbodysize struct {
	daprd *daprd.Daprd
	store state.Store
}

func (m *maxbodysize) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("skipping unix socket based test on windows")
	}

	socket := socket.New(t)

	m.store = inmemory.New(t)
	pluggable := statestore.New(t,
		statestore.WithSocket(socket),
		statestore.WithStateStore(m.store),
	)

	m.daprd = daprd.New(t,
		daprd.WithMaxBodySize("10Mi"),
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: pluggable
spec:
  type: state.%s
  version: v1
  metadata:
  - name: keyPrefix
    value: none
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: builtin
spec:
  type: state.in-memory
  version: v1
  metadata:
  - name: keyPrefix
    value: none
`, pluggable.SocketName())),
		daprd.WithSocket(t, socket),
	)

	return []framework.Option{
		framework.WithProcesses(pluggable, m.daprd),
	}
}

func (m *maxbodysize) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	client := m.daprd.GRPCClient(t, ctx)
	value := bytes.Repeat([]byte("x"), 5<<20)

	_, err := client.SaveState(ctx, &rtv1.SaveStateRequest{
		StoreName: "builtin",
		States:    []*commonv1.StateItem{{Key: "large", Value: value}},
	})
	require.NoError(t, err)
	require.NoError(t, m.store.Set(ctx, &state.SetRequest{Key: "large", Value: value}))

	for _, store := range []string{"builtin", "pluggable"} {
		resp, gerr := client.GetState(ctx, &rtv1.GetStateRequest{StoreName: store, Key: "large"})
		require.NoError(t, gerr, store)
		assert.Len(t, resp.GetData(), len(value), store)
	}

	tooLarge := bytes.Repeat([]byte("x"), 11<<20)
	const tooLargeErr = "received message larger than max"

	_, err = client.SaveState(ctx, &rtv1.SaveStateRequest{
		StoreName: "builtin",
		States:    []*commonv1.StateItem{{Key: "too-large", Value: tooLarge}},
	})
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
	require.ErrorContains(t, err, tooLargeErr)
	require.ErrorContains(t, err, "vs. 10485760)")

	require.NoError(t, m.store.Set(ctx, &state.SetRequest{Key: "too-large", Value: tooLarge}))
	_, err = client.GetState(ctx, &rtv1.GetStateRequest{StoreName: "pluggable", Key: "too-large"})
	assert.Equal(t, codes.Internal, status.Code(err))
	require.ErrorContains(t, err, "fail to get too-large from state store pluggable")
	require.ErrorContains(t, err, tooLargeErr)
	require.ErrorContains(t, err, "vs. 10485760)")
}
