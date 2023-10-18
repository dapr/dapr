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
	"os"
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

	secretStoreNames []string
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

	c.secretStoreNames = make([]string, numTests)
	for i := 0; i < numTests; i++ {
		fz.Fuzz(&c.secretStoreNames[i])
	}

	secretFileNames := util.FileNames(t, numTests)
	files := make([]string, numTests)
	for i, secretFileName := range secretFileNames {
		require.NoError(t, os.WriteFile(secretFileName, []byte("{}"), 0o600))

		files[i] = fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: '%s'
spec:
  type: secretstores.local.file
  version: v1
  metadata:
  - name: secretsFile
    value: '%s'
`,
			// Escape single quotes in the store name.
			strings.ReplaceAll(c.secretStoreNames[i], "'", "''"),
			strings.ReplaceAll(secretFileName, "'", "''"),
		)
	}

	c.daprd = procdaprd.New(t, procdaprd.WithResourceFiles(files...))

	return []framework.Option{
		framework.WithProcesses(c.daprd),
	}
}

func (c *componentName) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	pt := util.NewParallel(t)
	for _, secretStoreName := range c.secretStoreNames {
		secretStoreName := secretStoreName
		pt.Add(func(t *assert.CollectT) {
			getURL := fmt.Sprintf("http://localhost:%d/v1.0/secrets/%s/key1", c.daprd.HTTPPort(), url.QueryEscape(secretStoreName))
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
			require.NoError(t, err)
			resp, err := client.Do(req)
			require.NoError(t, err)
			// TODO: @joshvanl: 500 is obviously the wrong status code here and
			// should be changed.
			assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
			respBody, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Contains(t, string(respBody), "ERR_SECRET_GET")
			assert.Contains(t, string(respBody), "secret key1 not found")
		})
	}
}
