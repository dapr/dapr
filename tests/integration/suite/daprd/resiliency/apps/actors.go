/*
Copyright 2024 The Dapr Authors
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

package apps

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	daprdactors "github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(actors))
}

type actors struct {
	actors1 *daprdactors.Actors
	actors2 *daprdactors.Actors
	called1 atomic.Int64
	called2 atomic.Int64
}

func (a *actors) Setup(t *testing.T) []framework.Option {
	a.called1.Store(0)
	a.called2.Store(0)

	a.actors1 = daprdactors.New(t,
		daprdactors.WithActorTypes("abc"),
		daprdactors.WithActorTypeHandler("abc", func(w http.ResponseWriter, req *http.Request) {
			if a.called1.Add(1) > 3 {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
		}),
		daprdactors.WithResources(`apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: resiliency
spec:
  policies:
    timeouts:
      fast: 500ms
    retries:
      fiveRetries:
        policy: constant
        duration: 10ms
        maxRetries: 5
  targets:
    actors:
      abc:
        timeout: fast
        retry: fiveRetries
      def:
        timeout: fast
        retry: fiveRetries
`))

	a.actors2 = daprdactors.New(t,
		daprdactors.WithPeerActor(a.actors1),
		daprdactors.WithActorTypes("def"),
		daprdactors.WithActorTypeHandler("def", func(w http.ResponseWriter, req *http.Request) {
			if a.called2.Add(1) > 2 {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(a.actors1, a.actors2),
	}
}

func (a *actors) Run(t *testing.T, ctx context.Context) {
	a.actors1.WaitUntilRunning(t, ctx)
	a.actors2.WaitUntilRunning(t, ctx)

	url := fmt.Sprintf("http://%s/v1.0/actors/abc/ii/method/foo", a.actors1.Daprd().HTTPAddress())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	require.NoError(t, err)
	resp, err := client.HTTP(t).Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int64(4), a.called1.Load())

	url = fmt.Sprintf("http://%s/v1.0/actors/def/ii/method/foo", a.actors1.Daprd().HTTPAddress())
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	require.NoError(t, err)
	resp, err = client.HTTP(t).Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int64(3), a.called2.Load())
}
