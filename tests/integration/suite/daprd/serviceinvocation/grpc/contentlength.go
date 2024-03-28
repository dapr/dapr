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
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(contentlength))
}

type contentlength struct {
	daprd1 *procdaprd.Daprd
	daprd2 *procdaprd.Daprd
}

func (c *contentlength) Setup(t *testing.T) []framework.Option {
	app := app.New(t,
		app.WithHandlerFunc("/hi", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "OK")
		}),
	)

	c.daprd1 = procdaprd.New(t, procdaprd.WithAppPort(app.Port()))
	c.daprd2 = procdaprd.New(t, procdaprd.WithAppPort(app.Port()))

	return []framework.Option{
		framework.WithProcesses(app, c.daprd1, c.daprd2),
	}
}

func (c *contentlength) Run(t *testing.T, ctx context.Context) {
	c.daprd1.WaitUntilRunning(t, ctx)
	c.daprd2.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/hi", c.daprd1.HTTPAddress(), c.daprd2.AppID())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader("helloworld"))
	require.NoError(t, err)
	req.Header.Set("content-length", "1024")

	resp, err := client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	gclient := c.daprd1.GRPCClient(t, ctx)
	_, err = gclient.InvokeService(ctx, &rtv1.InvokeServiceRequest{
		Id: c.daprd2.AppID(),
		Message: &commonv1.InvokeRequest{
			Method:        "hi",
			HttpExtension: &commonv1.HTTPExtension{Verb: commonv1.HTTPExtension_GET},
		},
	})
	require.NoError(t, err)
}
