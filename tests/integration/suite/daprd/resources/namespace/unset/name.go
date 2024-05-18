/*
Copyright 2024 The Dapr Authors
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

package unset

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(name))
}

// name ensures that when the NAMESPACE env var is *not* set, all components will
// be loaded into as the default namespace, regardless of whether they have a
// namespace set in their metadata.
type name struct {
	daprd *daprd.Daprd
}

func (n *name) Setup(t *testing.T) []framework.Option {
	n.daprd = daprd.New(t, daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: abc
 namespace: abc
spec:
 type: state.in-memory
 version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 123
spec:
 type: state.in-memory
 version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 456
 namespace: foobar
spec:
 type: state.in-memory
 version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 789
 namespace: mynamespace
spec:
 type: state.in-memory
 version: v1
`))
	return []framework.Option{
		framework.WithProcesses(n.daprd),
	}
}

func (n *name) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)

	resp, err := n.daprd.GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.ElementsMatch(t, []*rtv1.RegisteredComponents{
		{
			Name: "abc", Type: "state.in-memory", Version: "v1",
			Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
		},
		{
			Name: "123", Type: "state.in-memory", Version: "v1",
			Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
		},
		{
			Name: "456", Type: "state.in-memory", Version: "v1",
			Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
		},
		{
			Name: "789", Type: "state.in-memory", Version: "v1",
			Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
		},
	}, resp.GetRegisteredComponents())
}
