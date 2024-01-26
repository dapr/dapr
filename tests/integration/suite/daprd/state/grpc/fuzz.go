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

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
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

type fuzzstate struct {
	daprd *procdaprd.Daprd

	storeName           string
	getFuzzKeys         []string
	saveReqBinaries     [][]*commonv1.StateItem
	saveReqBinariesHTTP [][]*commonv1.StateItem
	saveReqStrings      [][]*commonv1.StateItem
}

func (f *fuzzstate) Setup(t *testing.T) []framework.Option {
	const numTests = 1000

	var takenKeys sync.Map

	fuzzFuncs := []any{
		func(s *saveReqBinary, c fuzz.Continue) {
			var ok bool
			for len(s.Key) == 0 || strings.Contains(s.Key, "||") || ok {
				s.Key = c.RandString()
				_, ok = takenKeys.LoadOrStore(s.Key, true)
			}
			for len(s.Value) == 0 {
				c.Fuzz(&s.Value)
			}
		},
		func(s *saveReqString, c fuzz.Continue) {
			var ok bool
			for len(s.Key) == 0 || strings.Contains(s.Key, "||") || ok {
				s.Key = c.RandString()
				_, ok = takenKeys.LoadOrStore(s.Key, true)
			}
			for len(s.Value) == 0 {
				s.Value = c.RandString()
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
	f.saveReqBinaries = make([][]*commonv1.StateItem, numTests)
	f.saveReqBinariesHTTP = make([][]*commonv1.StateItem, numTests)
	f.saveReqStrings = make([][]*commonv1.StateItem, numTests)

	fz := fuzz.New().Funcs(fuzzFuncs...)
	for i := 0; i < numTests; i++ {
		fz.Fuzz(&f.getFuzzKeys[i])
		if strings.Contains(f.getFuzzKeys[i], "||") || len(path.IsValidPathSegmentName(f.getFuzzKeys[i])) > 0 {
			f.getFuzzKeys[i] = ""
			i--
		}
	}
	for i := 0; i < numTests; i++ {
		var (
			srb     []saveReqBinary
			srbHTTP []saveReqBinary
			srs     []saveReqString
		)

		fz.Fuzz(&srb)
		fz.Fuzz(&srbHTTP)
		fz.Fuzz(&srs)

		for j := range srb {
			f.saveReqBinaries[i] = append(f.saveReqBinaries[i], &commonv1.StateItem{
				Key:   srb[j].Key,
				Value: srb[j].Value,
			})
		}

		for j := range srbHTTP {
			f.saveReqBinariesHTTP[i] = append(f.saveReqBinariesHTTP[i], &commonv1.StateItem{
				Key:   srbHTTP[j].Key,
				Value: srbHTTP[j].Value,
			})
		}

		for j := range srs {
			f.saveReqStrings[i] = append(f.saveReqStrings[i], &commonv1.StateItem{
				Key:   srs[j].Key,
				Value: []byte(srs[j].Value),
			})
		}
	}

	return []framework.Option{
		framework.WithProcesses(f.daprd),
	}
}

func (f *fuzzstate) Run(t *testing.T, ctx context.Context) {
	f.daprd.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, util.GetMetaComponents(c, ctx, util.HTTPClient(t), f.daprd.HTTPPort()), 1)
	}, time.Second*20, time.Millisecond*100)

	client := f.daprd.GRPCClient(t, ctx)

	t.Run("get", func(t *testing.T) {
		pt := util.NewParallel(t)
		for i := range f.getFuzzKeys {
			i := i
			pt.Add(func(t *assert.CollectT) {
				resp, err := client.GetState(ctx, &rtv1.GetStateRequest{
					StoreName: f.storeName,
					Key:       f.getFuzzKeys[i],
				})
				require.NoError(t, err)
				assert.Empty(t, resp.GetData(), "key: %s", f.getFuzzKeys[i])
			})
		}
	})

	httpClient := util.HTTPClient(t)

	pt := util.NewParallel(t)
	for i := 0; i < len(f.getFuzzKeys); i++ {
		i := i
		pt.Add(func(t *assert.CollectT) {
			for _, req := range [][]*commonv1.StateItem{f.saveReqBinaries[i], f.saveReqStrings[i]} {
				_, err := client.SaveState(ctx, &rtv1.SaveStateRequest{
					StoreName: f.storeName,
					States:    req,
				})
				require.NoError(t, err)
			}

			postURL := fmt.Sprintf("http://localhost:%d/v1.0/state/%s", f.daprd.HTTPPort(), url.QueryEscape(f.storeName))
			b := new(bytes.Buffer)
			require.NoError(t, json.NewEncoder(b).Encode(f.saveReqBinariesHTTP[i]))
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, b)
			require.NoError(t, err)
			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			assert.Equalf(t, http.StatusNoContent, resp.StatusCode, "key: %s", url.QueryEscape(f.storeName))
			respBody, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Empty(t, string(respBody))

			for _, s := range append(f.saveReqStrings[i], f.saveReqBinaries[i]...) {
				resp, err := client.GetState(ctx, &rtv1.GetStateRequest{
					StoreName: f.storeName,
					Key:       s.GetKey(),
				})
				require.NoError(t, err)
				assert.Equalf(t, s.GetValue(), resp.GetData(), "orig=%s got=%s", s.GetValue(), resp.GetData())
			}

			for _, s := range f.saveReqBinariesHTTP[i] {
				resp, err := client.GetState(ctx, &rtv1.GetStateRequest{
					StoreName: f.storeName,
					Key:       s.GetKey(),
				})
				require.NoError(t, err)
				// TODO: Even though we are getting gRPC, the binary data was stored
				// with HTTP, so it was base64 encoded.
				val := `"` + base64.StdEncoding.EncodeToString(s.GetValue()) + `"`
				assert.Equalf(t, val, string(resp.GetData()), "orig=%s got=%s", val, resp.GetData())
			}
		})

		// TODO: Delete, eTag & Bulk APIs
	}
}
