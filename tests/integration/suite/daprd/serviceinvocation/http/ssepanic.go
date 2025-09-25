/*
Copyright 2025 The Dapr Authors
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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(ssepanic))
}

type ssepanic struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
}

func (s *ssepanic) Setup(t *testing.T) []framework.Option {
	srv := app.New(t)
	s.daprd1 = daprd.New(t, daprd.WithAppPort(srv.Port()))
	s.daprd2 = daprd.New(t, daprd.WithAppPort(srv.Port()))

	return []framework.Option{
		framework.WithProcesses(srv, s.daprd1, s.daprd2),
	}
}

func (s *ssepanic) Run(t *testing.T, ctx context.Context) {
	s.daprd1.WaitUntilRunning(t, ctx)
	s.daprd2.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://%s/v1.0/invoke/%s/method/foo", s.daprd1.HTTPAddress(), s.daprd2.AppID()), nil)
	require.NoError(t, err)

	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	require.NoError(t, err)

	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.JSONEq(t, fmt.Sprintf(
		`{"errorCode":"ERR_DIRECT_INVOKE","message":"failed to invoke, id: %s, err: rpc error: code = Internal desc = error invoking app channel: no response received from stream"}`,
		s.daprd2.AppID(),
	), string(b))
	require.NoError(t, resp.Body.Close())
}
