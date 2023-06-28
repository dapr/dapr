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
	"fmt"
	"regexp"
	"strings"
	"testing"

	fuzz "github.com/google/gofuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/apimachinery/pkg/api/validation/path"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(componentName))
}

type componentName struct {
	daprd *procdaprd.Daprd

	storeNames []string
}

func (c *componentName) Setup(t *testing.T) []framework.Option {
	const numTests = 1000
	takenNames := make(map[string]bool)

	reg, err := regexp.Compile("^([a-zA-Z].*)$")
	require.NoError(t, err)

	fz := fuzz.New().Funcs(func(s *string, c fuzz.Continue) {
		for *s == "" ||
			takenNames[*s] ||
			len(path.IsValidPathSegmentName(*s)) > 0 ||
			!reg.MatchString(*s) {
			*s = c.RandString()
		}
		takenNames[*s] = true
	})

	c.storeNames = make([]string, numTests)
	files := make([]string, numTests)
	for i := 0; i < numTests; i++ {
		fz.Fuzz(&c.storeNames[i])

		files[i] = fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: '%s'
spec:
  type: state.in-memory
  version: v1
`,
			// Escape single quotes in the store name.
			strings.ReplaceAll(c.storeNames[i], "'", "''"))
	}

	c.daprd = procdaprd.New(t, procdaprd.WithComponentFiles(files...))

	return []framework.Option{
		framework.WithProcesses(c.daprd),
	}
}

func (c *componentName) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	for _, storeName := range c.storeNames {
		storeName := storeName
		t.Run(storeName, func(t *testing.T) {
			t.Parallel()

			conn, err := grpc.DialContext(ctx, fmt.Sprintf("localhost:%d", c.daprd.GRPCPort()), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, conn.Close()) })

			client := rtv1.NewDaprClient(conn)

			_, err = client.SaveState(ctx, &rtv1.SaveStateRequest{
				StoreName: storeName,
				States: []*commonv1.StateItem{
					{Key: "key1", Value: []byte("value1")},
					{Key: "key2", Value: []byte("value2")},
				},
			})
			require.NoError(t, err)

			_, err = client.SaveState(ctx, &rtv1.SaveStateRequest{
				StoreName: storeName,
				States: []*commonv1.StateItem{
					{Key: "key1", Value: []byte("value1")},
					{Key: "key2", Value: []byte("value2")},
				},
			})
			require.NoError(t, err)

			resp, err := client.GetState(ctx, &rtv1.GetStateRequest{
				StoreName: storeName,
				Key:       "key1",
			})
			require.NoError(t, err)
			assert.Equal(t, "value1", string(resp.Data))

			resp, err = client.GetState(ctx, &rtv1.GetStateRequest{
				StoreName: storeName,
				Key:       "key2",
			})
			require.NoError(t, err)
			assert.Equal(t, "value2", string(resp.Data))
		})
	}
}
