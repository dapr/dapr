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

package appapitoken

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(selfwithtoken))
}

type selfwithtoken struct {
	daprd *daprd.Daprd
	ch    chan http.Header
}

func (s *selfwithtoken) Setup(t *testing.T) []framework.Option {
	s.ch = make(chan http.Header, 1)
	app := app.New(t,
		app.WithHandlerFunc("/helloworld", func(w http.ResponseWriter, r *http.Request) {
			s.ch <- r.Header
		}),
	)

	s.daprd = daprd.New(t,
		daprd.WithAppAPIToken(t, "abc"),
		daprd.WithAppPort(app.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(app, s.daprd),
	}
}

func (s *selfwithtoken) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	dclient := s.daprd.GRPCClient(t, ctx)
	_, err := dclient.InvokeService(ctx, &runtimev1.InvokeServiceRequest{
		Id: s.daprd.AppID(),
		Message: &commonv1.InvokeRequest{
			Method:        "helloworld",
			Data:          new(anypb.Any),
			HttpExtension: &commonv1.HTTPExtension{Verb: commonv1.HTTPExtension_GET},
		},
	})
	require.NoError(t, err)

	select {
	case header := <-s.ch:
		require.Equal(t, "abc", header.Get("dapr-api-token"))
	case <-time.After(5 * time.Second):
		assert.Fail(t, "timed out waiting for header")
	}
}
