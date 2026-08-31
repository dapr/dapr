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

package call

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(actorACL))
}

// actorACL verifies that path traversal in actor method names cannot escape
// the /actors/{type}/{id}/method/ prefix to reach non-actor endpoints on
// the app.
type actorACL struct {
	app *actors.Actors

	secretHits atomic.Int64
	rootHits   atomic.Int64
}

func (a *actorACL) Setup(t *testing.T) []framework.Option {
	a.app = actors.New(t,
		actors.WithActorTypes("mytype"),
		actors.WithActorTypeHandler("mytype", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			w.Write([]byte("actor:" + r.URL.Path))
		}),
		actors.WithHandler("/secret", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			a.secretHits.Add(1)
			w.Write([]byte("secret"))
		}),
		actors.WithHandler("/", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			if r.URL.Path == "/secret" {
				a.secretHits.Add(1)
			}
			a.rootHits.Add(1)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(a.app),
	}
}

func (a *actorACL) Run(t *testing.T, ctx context.Context) {
	a.app.WaitUntilRunning(t, ctx)
	a.app.Placement().WaitUntilRunning(t, ctx)

	grpcClient := a.app.GRPCClient(t, ctx)
	httpClient := client.HTTP(t)

	a.secretHits.Store(0)
	a.rootHits.Store(0)

	grpcInvoke := func(t *testing.T, method string) error {
		t.Helper()
		_, err := grpcClient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "mytype",
			ActorId:   "1",
			Method:    method,
		})
		return err
	}

	httpInvoke := func(t *testing.T, methodSuffix string) int {
		t.Helper()
		url := fmt.Sprintf(
			"http://%s/v1.0/actors/mytype/1/method/%s",
			a.app.Daprd().HTTPAddress(),
			methodSuffix,
		)
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		io.ReadAll(resp.Body)
		return resp.StatusCode
	}

	// gRPC: path traversal that would escape actor prefix.
	// Without NormalizeMethod, "../../../../secret" would build:
	// actors/mytype/1/method/../../../../secret → path.Clean → /secret
	// With NormalizeMethod, it resolves to "secret" before path construction:
	// actors/mytype/1/method/secret → stays within actor prefix.

	t.Run("grpc: deep traversal to /secret", func(t *testing.T) {
		// NormalizeMethod resolves ../../../../secret → secret.
		// The app receives /actors/mytype/1/method/secret, not /secret.
		err := grpcInvoke(t, "../../../../secret")
		require.NoError(t, err)
	})

	t.Run("grpc: traversal with method prefix escape", func(t *testing.T) {
		err := grpcInvoke(t, "../../../secret")
		require.NoError(t, err)
	})

	t.Run("grpc: relative traversal to secret", func(t *testing.T) {
		err := grpcInvoke(t, "foo/../../../secret")
		require.NoError(t, err)
	})

	// HTTP: encoded traversal attempts.
	t.Run("http: deep traversal to /secret", func(t *testing.T) {
		status := httpInvoke(t, "..%2F..%2F..%2F..%2Fsecret")
		assert.Equal(t, nethttp.StatusOK, status)
	})

	t.Run("http: traversal with method prefix escape", func(t *testing.T) {
		status := httpInvoke(t, "..%2F..%2F..%2Fsecret")
		assert.Equal(t, nethttp.StatusOK, status)
	})

	// Core invariant: the /secret handler must NEVER have been reached
	// by any of the traversal attacks above.
	t.Run("/secret handler was never reached", func(t *testing.T) {
		assert.Equalf(t, int64(0), a.secretHits.Load(),
			"/secret handler was hit %d times — actor method traversal escaped actor prefix!",
			a.secretHits.Load())
	})
}
