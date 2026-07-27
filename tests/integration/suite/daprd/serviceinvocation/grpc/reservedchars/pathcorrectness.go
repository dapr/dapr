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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"google.golang.org/protobuf/types/known/anypb"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(pathcorrectness))
}

type pathcorrectness struct {
	caller *procdaprd.Daprd
	callee *procdaprd.Daprd
}

func (r *pathcorrectness) Setup(t *testing.T) []framework.Option {
	onInvoke := func(ctx context.Context, in *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
		return &commonv1.InvokeResponse{
			Data: &anypb.Any{Value: []byte(in.GetMethod())},
		}, nil
	}

	srv := app.New(t, app.WithOnInvokeFn(onInvoke))
	r.callee = procdaprd.New(t,
		procdaprd.WithAppProtocol("grpc"),
		procdaprd.WithAppPort(srv.Port(t)),
	)
	r.caller = procdaprd.New(t)

	return []framework.Option{
		framework.WithProcesses(srv, r.callee, r.caller),
	}
}

func (r *pathcorrectness) Run(t *testing.T, ctx context.Context) {
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

	// Methods containing #, ?, or \x00 are rejected by NormalizeMethod.
	// (% is no longer forbidden since NormalizeMethod no longer decodes.)
	rejectedTests := []struct {
		name   string
		method string
	}{
		{name: "hash in method", method: "test#stream"},
		{name: "question mark in method", method: "test?stream"},
		{name: "null byte in method", method: "test\x00stream"},
		{name: "carriage return in method", method: "test\rstream"},
		{name: "multiple reserved characters", method: "a#b?c%d"},
	}

	for _, tt := range rejectedTests {
		t.Run(tt.name, func(t *testing.T) {
			_, code := invoke(t, tt.method)
			assert.NotEqualf(t, codes.OK, code,
				"method %q should be rejected", tt.method)
		})
	}

	// Safe characters pass through normally. NormalizeMethod no longer
	// decodes percent-encoding, so % and percent-encoded sequences pass
	// through as raw strings.
	safeTests := []struct {
		name           string
		method         string
		expectedMethod string
	}{
		{
			name:           "double quote in method",
			method:         `test"stream`,
			expectedMethod: `test"stream`,
		},
		{
			name:           "asterisk in method",
			method:         "test*stream",
			expectedMethod: "test*stream",
		},
		{
			name:           "backslash in method",
			method:         `test\stream`,
			expectedMethod: `test\stream`,
		},
		{
			name:           "percent in method",
			method:         "test%stream",
			expectedMethod: "test%stream",
		},
		{
			name:           "encoded percent in method",
			method:         "test%25stream",
			expectedMethod: "test%25stream",
		},
	}

	for _, tt := range safeTests {
		t.Run(tt.name, func(t *testing.T) {
			got, code := invoke(t, tt.method)
			assert.Equalf(t, codes.OK, code, "method %q should pass through normally", tt.method)
			assert.Equalf(t, tt.expectedMethod, got,
				"callee should receive the exact method string sent by caller via gRPC")
		})
	}

	// Path traversal is resolved before dispatch; callee gets the clean path.
	t.Run("path traversal", func(t *testing.T) {
		got, code := invoke(t, "admin/../public")
		assert.Equalf(t, codes.OK, code, "path traversal should be resolved, not rejected")
		assert.Equalf(t, "public", got,
			"callee should receive the resolved path, not the raw traversal")
	})

	// Percent-encoded slashes (%2F) are literal characters in gRPC.
	// They are NOT decoded — callee receives them as-is.
	t.Run("encoded slash preserved in method", func(t *testing.T) {
		got, code := invoke(t, "test%2Fstream")
		assert.Equalf(t, codes.OK, code, "method %q should pass through normally", "test%2Fstream")
		assert.Equalf(t, "test%2Fstream", got,
			"callee should receive literal %%2F, not a decoded slash")
	})

	t.Run("encoded slash traversal preserved literally", func(t *testing.T) {
		// admin%2F..%2Fpublic has no real '/' — callee receives it as-is.
		got, code := invoke(t, "admin%2F..%2Fpublic")
		assert.Equalf(t, codes.OK, code, "method %q should pass through normally", "admin%2F..%2Fpublic")
		assert.Equalf(t, "admin%2F..%2Fpublic", got,
			"callee should receive literal percent-encoded slashes, not decoded")
	})

	t.Run("double traversal resolved", func(t *testing.T) {
		got, code := invoke(t, "admin/../../public")
		assert.Equalf(t, codes.OK, code, "double traversal should be resolved")
		assert.Equalf(t, "public", got,
			"extra ../ should be clamped, callee receives clean path")
	})

	t.Run("trailing slash cleaned", func(t *testing.T) {
		got, code := invoke(t, "public/")
		assert.Equalf(t, codes.OK, code, "trailing slash should be cleaned")
		assert.Equalf(t, "public", got,
			"trailing slash should be removed by path.Clean")
	})

	t.Run("duplicate slashes cleaned", func(t *testing.T) {
		got, code := invoke(t, "api///v1///users")
		assert.Equalf(t, codes.OK, code, "duplicate slashes should be cleaned")
		assert.Equalf(t, "api/v1/users", got,
			"duplicate slashes should be collapsed by path.Clean")
	})
}
