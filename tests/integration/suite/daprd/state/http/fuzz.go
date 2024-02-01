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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

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
	suite.Register(new(fuzzstate))
}

type saveReqBinary struct {
	Key   string `json:"key"`
	Value []byte `json:"value"`
}

type saveReqString struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type saveReqAny struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

type fuzzstate struct {
	daprd *procdaprd.Daprd

	storeName       string
	getFuzzKeys     []string
	saveReqBinaries [][]saveReqBinary
	saveReqStrings  [][]saveReqString
	saveReqAnys     [][]saveReqAny
}

func (f *fuzzstate) Setup(t *testing.T) []framework.Option {
	const numTests = 1000

	var takenKeys sync.Map

	fuzzFuncs := []any{
		func(s *saveReqBinary, c fuzz.Continue) {
			var ok bool
			for len(s.Key) == 0 || strings.Contains(s.Key, "||") || s.Key == "." || ok {
				s.Key = c.RandString()
				_, ok = takenKeys.LoadOrStore(s.Key, true)
			}
			for len(s.Value) == 0 {
				c.Fuzz(&s.Value)
			}
		},
		func(s *saveReqString, c fuzz.Continue) {
			var ok bool
			for len(s.Key) == 0 || strings.Contains(s.Key, "||") || s.Key == "." || ok {
				s.Key = c.RandString()
				_, ok = takenKeys.LoadOrStore(s.Key, true)
			}
			for len(s.Value) == 0 {
				s.Value = c.RandString()
			}
		},
		func(s *saveReqAny, c fuzz.Continue) {
			var ok bool
			for len(s.Key) == 0 || strings.Contains(s.Key, "||") || s.Key == "." || ok {
				s.Key = c.RandString()
				_, ok = takenKeys.LoadOrStore(s.Key, true)
			}
		},
		func(s *string, c fuzz.Continue) {
			var ok bool
			for len(*s) == 0 || ok {
				*s = c.RandString()
				_, ok = takenKeys.LoadOrStore(*s, true)
			}
		},
	}

	for f.storeName == "" ||
		len(path.IsValidPathSegmentName(f.storeName)) > 0 {
		fuzz.New().Fuzz(&f.storeName)
	}

	f.daprd = procdaprd.New(t, procdaprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: '%s'
spec:
  type: state.in-memory
  version: v1
`, strings.ReplaceAll(f.storeName, "'", "''"))))

	f.getFuzzKeys = make([]string, numTests)
	f.saveReqBinaries = make([][]saveReqBinary, numTests)
	f.saveReqStrings = make([][]saveReqString, numTests)
	f.saveReqAnys = make([][]saveReqAny, numTests)

	fz := fuzz.New().Funcs(fuzzFuncs...)
	for i := 0; i < numTests; i++ {
		fz.Fuzz(&f.getFuzzKeys[i])
		// Prevent invalid names
		if strings.Contains(f.getFuzzKeys[i], "||") || f.getFuzzKeys[i] == "." || len(path.IsValidPathSegmentName(f.getFuzzKeys[i])) > 0 {
			f.getFuzzKeys[i] = ""
			i--
		}
	}
	for i := 0; i < numTests; i++ {
		fz.Fuzz(&f.saveReqBinaries[i])
		fz.Fuzz(&f.saveReqStrings[i])
		fz.Fuzz(&f.saveReqAnys[i])
	}

	return []framework.Option{
		framework.WithProcesses(f.daprd),
	}
}

func (f *fuzzstate) Run(t *testing.T, ctx context.Context) {
	f.daprd.WaitUntilRunning(t, ctx)

	httpClient := util.HTTPClient(t)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, util.GetMetaComponents(c, ctx, httpClient, f.daprd.HTTPPort()), 1)
	}, time.Second*20, time.Millisecond*100)

	t.Run("get", func(t *testing.T) {
		pt := util.NewParallel(t)
		for i := range f.getFuzzKeys {
			i := i
			pt.Add(func(t *assert.CollectT) {
				getURL := fmt.Sprintf("http://localhost:%d/v1.0/state/%s/%s", f.daprd.HTTPPort(), url.QueryEscape(f.storeName), url.QueryEscape(f.getFuzzKeys[i]))
				// t.Log("URL", getURL)
				// t.Log("State store name", f.storeName, hex.EncodeToString([]byte(f.storeName)), printRunes(f.storeName))
				// t.Log("Key", f.getFuzzKeys[i], printRunes(f.getFuzzKeys[i]))
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
				require.NoError(t, err)
				resp, err := httpClient.Do(req)
				require.NoError(t, err)
				assert.Equal(t, http.StatusNoContent, resp.StatusCode)
				respBody, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				assert.Empty(t, string(respBody), "key: %s", f.getFuzzKeys[i])
			})
		}
	})

	pt := util.NewParallel(t)
	for i := 0; i < len(f.getFuzzKeys); i++ {
		i := i
		pt.Add(func(t *assert.CollectT) {
			for _, req := range []any{f.saveReqBinaries[i], f.saveReqStrings[i]} {
				postURL := fmt.Sprintf("http://localhost:%d/v1.0/state/%s", f.daprd.HTTPPort(), url.QueryEscape(f.storeName))
				b := new(bytes.Buffer)
				require.NoError(t, json.NewEncoder(b).Encode(req))
				// t.Log("URL", postURL)
				// t.Log("State store name", f.storeName, hex.EncodeToString([]byte(f.storeName)), printRunes(f.storeName))
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, b)
				require.NoError(t, err)
				resp, err := httpClient.Do(req)
				require.NoError(t, err)
				assert.Equalf(t, http.StatusNoContent, resp.StatusCode, "key: %s", url.QueryEscape(f.storeName))
				respBody, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				assert.Empty(t, string(respBody))
			}

			for _, s := range f.saveReqBinaries[i] {
				getURL := fmt.Sprintf("http://localhost:%d/v1.0/state/%s/%s", f.daprd.HTTPPort(), url.QueryEscape(f.storeName), url.QueryEscape(s.Key))
				// t.Log("URL", getURL)
				// t.Log("State store name", f.storeName, hex.EncodeToString([]byte(f.storeName)), printRunes(f.storeName))
				// t.Log("Key", s.Key, hex.EncodeToString([]byte(s.Key)), printRunes(s.Key))
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
				require.NoError(t, err)
				resp, err := httpClient.Do(req)
				require.NoError(t, err)
				assert.Equal(t, http.StatusOK, resp.StatusCode)
				// TODO: @joshvanl: document the fact that saving binary state will HTTP will be base64
				// encoded.
				respBody, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				val := `"` + base64.StdEncoding.EncodeToString(s.Value) + `"`
				assert.Equalf(t, val, string(respBody), "key: %s, %s", s.Key, req.URL.String())
			}

			for _, s := range f.saveReqStrings[i] {
				getURL := fmt.Sprintf("http://localhost:%d/v1.0/state/%s/%s", f.daprd.HTTPPort(), url.QueryEscape(f.storeName), url.QueryEscape(s.Key))
				// t.Log("URL", getURL)
				// t.Log("State store name", f.storeName, hex.EncodeToString([]byte(f.storeName)), printRunes(f.storeName))
				// t.Log("Key", s.Key, hex.EncodeToString([]byte(s.Key)), printRunes(s.Key))
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
				require.NoError(t, err)
				resp, err := httpClient.Do(req)
				require.NoError(t, err)
				assert.Equal(t, http.StatusOK, resp.StatusCode)
				respBody, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				// Some state stores (such as in-memory that we are using here) mangle
				// the string by HTML escaping it, which changes specifical characters
				// such as <, >, &, to \u003c, \u003e, \u0026, etc. This is not the
				// case for other state stores.
				// https://pkg.go.dev/encoding/json#HTMLEscape
				js, err := json.Marshal(s.Value)
				require.NoError(t, err)
				var orig bytes.Buffer
				json.HTMLEscape(&orig, js)
				assert.Equalf(t, orig.Bytes(), respBody, "orig=%s got=%s", orig, respBody)
			}
		})

		// TODO: Delete, eTag & Bulk APIs
	}
}

/*
func printRunes(str string) []string {
	result := make([]string, 0, len(str))
	for _, r := range str {
		result = append(result, strconv.Itoa(int(r)))
	}
	return result
}
*/
