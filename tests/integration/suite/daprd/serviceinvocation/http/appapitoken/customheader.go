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

package appapitoken

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/anypb"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(customheader))
}

type customheader struct {
	withToken *daprd.Daprd
	noToken   *daprd.Daprd
	ch        chan http.Header
}

func (c *customheader) Setup(t *testing.T) []framework.Option {
	c.ch = make(chan http.Header, 2)
	app := app.New(t,
		app.WithHandlerFunc("/helloworld", func(w http.ResponseWriter, r *http.Request) {
			c.ch <- r.Header
		}),
	)

	c.withToken = daprd.New(t,
		daprd.WithAppAPIToken(t, "abc"),
		daprd.WithAppAPITokenHeader(t, " X-API-Key "),
		daprd.WithAppPort(app.Port()),
	)
	c.noToken = daprd.New(t,
		daprd.WithAppAPITokenHeader(t, " X-API-Key "),
		daprd.WithAppPort(app.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(app, c.withToken, c.noToken),
	}
}

func (c *customheader) Run(t *testing.T, ctx context.Context) {
	c.withToken.WaitUntilRunning(t, ctx)
	c.noToken.WaitUntilRunning(t, ctx)

	invoke := func(daprdProcess *daprd.Daprd) {
		t.Helper()
		invokeCtx := metadata.AppendToOutgoingContext(ctx,
			"dapr-api-token", "default-oldtoken",
			"x-api-key", "custom-oldtoken",
		)
		_, err := daprdProcess.GRPCClient(t, ctx).InvokeService(invokeCtx, &runtimev1.InvokeServiceRequest{
			Id: daprdProcess.AppID(),
			Message: &commonv1.InvokeRequest{
				Method:        "helloworld",
				Data:          new(anypb.Any),
				HttpExtension: &commonv1.HTTPExtension{Verb: commonv1.HTTPExtension_GET},
			},
		})
		require.NoError(t, err)
	}

	invoke(c.withToken)
	select {
	case header := <-c.ch:
		assert.Empty(t, header.Values("dapr-api-token"))
		assert.Equal(t, "abc", header.Get("x-api-key"))
	case <-time.After(5 * time.Second):
		assert.Fail(t, "timed out waiting for custom token header")
	}

	invoke(c.noToken)
	select {
	case header := <-c.ch:
		assert.Empty(t, header.Values("dapr-api-token"))
		assert.Empty(t, header.Values("x-api-key"))
	case <-time.After(5 * time.Second):
		assert.Fail(t, "timed out waiting for header without a token")
	}
}
