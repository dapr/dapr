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

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(componentName))
}

type componentName struct {
	daprd *procdaprd.Daprd

	pubsubNames []string
	topicNames  []string
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

	c.pubsubNames = make([]string, numTests)
	c.topicNames = make([]string, numTests)
	files := make([]string, numTests)
	for i := 0; i < numTests; i++ {
		fz.Fuzz(&c.pubsubNames[i])
		fz.Fuzz(&c.topicNames[i])

		files[i] = fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: '%s'
spec:
  type: pubsub.in-memory
  version: v1
`,
			// Escape single quotes in the store name.
			strings.ReplaceAll(c.pubsubNames[i], "'", "''"))
	}

	c.daprd = procdaprd.New(t, procdaprd.WithResourceFiles(files...))

	return []framework.Option{
		framework.WithProcesses(c.daprd),
	}
}

func (c *componentName) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	pt := util.NewParallel(t)
	for i := range c.pubsubNames {
		pubsubName := c.pubsubNames[i]
		topicName := c.topicNames[i]
		pt.Add(func(col *assert.CollectT) {
			conn, err := grpc.DialContext(ctx, c.daprd.GRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
			require.NoError(col, err)
			t.Cleanup(func() { require.NoError(t, conn.Close()) })

			client := rtv1.NewDaprClient(conn)
			_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
				PubsubName: pubsubName,
				Topic:      topicName,
				Data:       []byte(`{"status": "completed"}`),
			})
			//nolint:testifylint
			assert.NoError(col, err)
		})
	}
}
