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
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	fuzz "github.com/google/gofuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/validation/path"

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

	httpClient := util.HTTPClient(t)

	pt := util.NewParallel(t)
	for i := range c.pubsubNames {
		pubsubName := c.pubsubNames[i]
		topicName := c.topicNames[i]
		pt.Add(func(t *assert.CollectT) {
			reqURL := fmt.Sprintf("http://127.0.0.1:%d/v1.0/publish/%s/%s", c.daprd.HTTPPort(), url.QueryEscape(pubsubName), url.QueryEscape(topicName))
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(`{"status": "completed"}`))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusNoContent, resp.StatusCode, reqURL)
			respBody, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Empty(t, string(respBody))
		})
	}
}
