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

package reservedchars

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(pathcorrectnesshttpapp))
}

// pathcorrectnesshttpapp exercises the gRPC Dapr API (InvokeService)
// invoking a callee whose app protocol is HTTP, so the method goes through
// constructRequest's URL building rather than the gRPC-to-gRPC in.GetMethod()
// passthrough covered by pathcorrectness.go. This proves the trailing-slash
// fix for dapr/dapr#7686 applies on this cross-protocol path too.
type pathcorrectnesshttpapp struct {
	caller *procdaprd.Daprd
	callee *procdaprd.Daprd
}

func (r *pathcorrectnesshttpapp) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(req.URL.Path))
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	r.callee = procdaprd.New(t, procdaprd.WithAppPort(srv.Port()))
	r.caller = procdaprd.New(t)

	return []framework.Option{
		framework.WithProcesses(srv, r.callee, r.caller),
	}
}

func (r *pathcorrectnesshttpapp) Run(t *testing.T, ctx context.Context) {
	r.caller.WaitUntilRunning(t, ctx)
	r.callee.WaitUntilRunning(t, ctx)

	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, r.caller.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	invoke := func(t *testing.T, method string) (string, codes.Code) {
		t.Helper()
		resp, err := client.InvokeService(ctx, &rtv1.InvokeServiceRequest{
			Id: r.callee.AppID(),
			Message: &commonv1.InvokeRequest{
				Method:        method,
				HttpExtension: &commonv1.HTTPExtension{Verb: commonv1.HTTPExtension_GET},
			},
		})
		if err != nil {
			return status.Convert(err).Message(), status.Convert(err).Code()
		}
		return string(resp.GetData().GetValue()), codes.OK
	}

	t.Run("trailing slash delivered to HTTP app", func(t *testing.T) {
		got, code := invoke(t, "foo/bar/")
		assert.Equalf(t, codes.OK, code, "should not be rejected")
		assert.Equalf(t, "/foo/bar/", got,
			"the HTTP app should receive the method with its trailing slash intact")
	})

	t.Run("no trailing slash stays without one", func(t *testing.T) {
		got, code := invoke(t, "foo/bar")
		assert.Equalf(t, codes.OK, code, "should not be rejected")
		assert.Equalf(t, "/foo/bar", got,
			"a method with no trailing slash must not gain one")
	})

	t.Run("traversal with trailing slash resolves and preserves slash", func(t *testing.T) {
		got, code := invoke(t, "admin/../public/")
		assert.Equalf(t, codes.OK, code, "should not be rejected")
		assert.Equalf(t, "/public/", got,
			"traversal should resolve and the trailing slash should survive")
	})
}
