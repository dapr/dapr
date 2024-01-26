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

package resources

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(namespace))
}

// namespace ensures that the component disk loader does not load components
// from different namespaces.
type namespace struct {
	daprd *daprd.Daprd
}

func (n *namespace) Setup(t *testing.T) []framework.Option {
	n.daprd = daprd.New(t, daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: abc
spec:
  type: state.in-memory
  version: v1
---
# This component is skipped because it is in a different namespace. Even though
# it has the same name, daprd will not error as it is not loaded.
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: abc
  namespace: notmynamespace
spec:
  type: state.in-memory
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: ghi
  namespace: notmynamespace
spec:
  type: state.in-memory
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: def
  namespace: mynamespace
spec:
  type: state.in-memory
  version: v1
`),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "NAMESPACE", "mynamespace"),
		),
	)
	return []framework.Option{
		framework.WithProcesses(n.daprd),
	}
}

func (n *namespace) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.DialContext(ctx, n.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	client := rtv1.NewDaprClient(conn)

	resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.ElementsMatch(t, []*rtv1.RegisteredComponents{
		{
			Name: "abc", Type: "state.in-memory", Version: "v1",
			Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
		},
		{
			Name: "def", Type: "state.in-memory", Version: "v1",
			Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
		},
	}, resp.GetRegisteredComponents())
}
