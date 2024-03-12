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
	"net/url"
	"strings"
	"testing"

	fuzz "github.com/google/gofuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/anypb"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(fuzzgrpc))
}

type fuzzgrpc struct {
	daprd1 *procdaprd.Daprd
	daprd2 *procdaprd.Daprd

	methods []string
	bodies  [][]byte
	queries []map[string]string
}

func (f *fuzzgrpc) Setup(t *testing.T) []framework.Option {
	const numTests = 1000

	onInvoke := func(ctx context.Context, in *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
		return &commonv1.InvokeResponse{
			Data: &anypb.Any{Value: in.GetData().GetValue()},
		}, nil
	}

	srv := newGRPCServer(t, onInvoke)
	f.daprd1 = procdaprd.New(t, procdaprd.WithAppProtocol("grpc"), procdaprd.WithAppPort(srv.Port(t)))
	f.daprd2 = procdaprd.New(t)

	f.methods = make([]string, numTests)
	f.bodies = make([][]byte, numTests)
	f.queries = make([]map[string]string, numTests)
	for i := 0; i < numTests; i++ {
		fz := fuzz.New()
		fz.NumElements(0, 100).Fuzz(&f.methods[i])
		fz.NumElements(0, 100).Fuzz(&f.bodies[i])
		fz.NumElements(0, 100).Fuzz(&f.queries[i])
	}

	return []framework.Option{
		framework.WithProcesses(f.daprd1, f.daprd2, srv),
	}
}

func (f *fuzzgrpc) Run(t *testing.T, ctx context.Context) {
	f.daprd1.WaitUntilRunning(t, ctx)
	f.daprd2.WaitUntilRunning(t, ctx)

	pt := util.NewParallel(t)
	for i := 0; i < len(f.methods); i++ {
		method := f.methods[i]
		body := f.bodies[i]
		query := f.queries[i]
		var queryString []string
		for k, v := range query {
			queryString = append(queryString, fmt.Sprintf("%s=%s", k, v))
		}

		pt.Add(func(c *assert.CollectT) {
			conn, err := grpc.DialContext(ctx, fmt.Sprintf("127.0.0.1:%d", f.daprd2.GRPCPort()), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
			require.NoError(c, err)
			t.Cleanup(func() { require.NoError(t, conn.Close()) })

			resp, err := rtv1.NewDaprClient(conn).InvokeService(ctx, &rtv1.InvokeServiceRequest{
				Id: f.daprd1.AppID(),
				Message: &commonv1.InvokeRequest{
					Method: url.QueryEscape(method),
					Data:   &anypb.Any{Value: body},
					HttpExtension: &commonv1.HTTPExtension{
						Verb:        commonv1.HTTPExtension_POST,
						Querystring: strings.Join(queryString, "&"),
					},
				},
			})
			require.NoError(c, err)
			if len(body) == 0 {
				body = nil
			}
			assert.Equal(c, body, resp.GetData().GetValue())
		})
	}
}
