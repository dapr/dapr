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

package subscriptions

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(noapp))
}

type noapp struct {
	daprd *daprd.Daprd
	dir   string
}

func (n *noapp) Setup(t *testing.T) []framework.Option {
	n.dir = t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(n.dir, "comp.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'pubsub'
spec:
 type: pubsub.in-memory
 version: v1
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: sub1
spec:
 pubsubname: pubsub
 topic: a
 routes:
  default: '/a'
`), 0o600))

	n.daprd = daprd.New(t,
		daprd.WithResourcesDir(n.dir),
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configun
metadata:
  name: hotreloading
spec:
  features:
  - name: HotReload
    enabled: true`),
	)

	return []framework.Option{
		framework.WithProcesses(n.daprd),
	}
}

func (n *noapp) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)
	assert.Len(t, n.daprd.GetMetaRegisteredComponents(t, ctx), 1)
	assert.Len(t, n.daprd.GetMetaSubscriptions(t, ctx), 1)

	_, err := n.daprd.GRPCClient(t, ctx).PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "pubsub",
		Topic:      "a",
		Data:       []byte("hello"),
	})
	require.NoError(t, err)
}
