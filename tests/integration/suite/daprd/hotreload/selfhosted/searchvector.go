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

package selfhosted

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(searchvector))
}

// searchvector covers loading, replacing and removing search and vector
// components while daprd is running.
type searchvector struct {
	daprd *daprd.Daprd

	resDir string
}

func (s *searchvector) Setup(t *testing.T) []framework.Option {
	s.resDir = t.TempDir()
	s.daprd = daprd.New(t,
		daprd.WithResourcesDir(s.resDir),
	)

	return []framework.Option{
		framework.WithProcesses(s.daprd),
	}
}

func (s *searchvector) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	client := s.daprd.GRPCClient(t, ctx)

	listIndexes := func() error {
		_, err := client.ListIndexesAlpha1(ctx, &rtv1.ListIndexesRequestAlpha1{StoreName: "search"})
		return err
	}
	listCollections := func() error {
		_, err := client.ListCollectionsAlpha1(ctx, &rtv1.ListCollectionsRequestAlpha1{StoreName: "vector"})
		return err
	}

	t.Run("no store is configured yet", func(t *testing.T) {
		assert.Equal(t, codes.FailedPrecondition, status.Code(listIndexes()))
		assert.Equal(t, codes.FailedPrecondition, status.Code(listCollections()))
	})

	t.Run("creating the components makes both stores available", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(s.resDir, "1.yaml"), []byte(
			s.component("search", "search.in-memory")+"\n---\n"+s.component("vector", "vector.in-memory"),
		), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.NoError(c, listIndexes())
			assert.NoError(c, listCollections())
		}, 10*time.Second, 10*time.Millisecond)
	})

	t.Run("changing the vector component type removes only that store", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(s.resDir, "1.yaml"), []byte(
			s.component("search", "search.in-memory")+"\n---\n"+`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: vector
spec:
  type: state.in-memory
  version: v1
`), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, codes.FailedPrecondition, status.Code(listCollections()))
		}, 10*time.Second, 10*time.Millisecond)
		require.NoError(t, listIndexes())
	})

	t.Run("deleting the components removes the search store", func(t *testing.T) {
		require.NoError(t, os.Remove(filepath.Join(s.resDir, "1.yaml")))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, codes.FailedPrecondition, status.Code(listIndexes()))
		}, 10*time.Second, 10*time.Millisecond)
		assert.Equal(t, codes.FailedPrecondition, status.Code(listCollections()))
	})
}

func (s *searchvector) component(name, typ string) string {
	return fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: %s
spec:
  type: %s
  version: v1
`, name, typ)
}
