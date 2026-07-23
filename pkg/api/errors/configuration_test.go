/*
Copyright 2024 The Dapr Authors
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

package errors

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/messages/errorcodes"
)

// configErrorInfo extracts the ErrorInfo detail from a standardized API error.
func configErrorInfo(t *testing.T, err error) *errdetails.ErrorInfo {
	t.Helper()
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "error should expose a gRPC status")
	for _, d := range st.Details() {
		if ei, ok := d.(*errdetails.ErrorInfo); ok {
			return ei
		}
	}
	t.Fatalf("no ErrorInfo detail found in error: %v", err)
	return nil
}

func TestConfigurationErrorsStandardized(t *testing.T) {
	c := Configuration("test-store")
	dummy := fmt.Errorf("boom")

	tests := []struct {
		name       string
		err        error
		wantCode   codes.Code
		wantReason string
	}{
		{"StoreNotConfigured", c.StoreNotConfigured(), codes.FailedPrecondition, string(errorcodes.ConfigurationStoreNotConfigured.GrpcCode)},
		{"StoreNotFound", c.StoreNotFound(), codes.InvalidArgument, string(errorcodes.ConfigurationStoreNotFound.GrpcCode)},
		{"GetFailed", c.GetFailed([]string{"key1"}, dummy), codes.Internal, string(errorcodes.ConfigurationGet.GrpcCode)},
		{"SubscribeFailed", c.SubscribeFailed([]string{"key1"}, dummy), codes.InvalidArgument, string(errorcodes.ConfigurationSubscribe.GrpcCode)},
		{"UnsubscribeFailed", c.UnsubscribeFailed("sub-1", dummy), codes.Internal, string(errorcodes.ConfigurationUnsubscribe.GrpcCode)},
		{"UnsubscribeNotFound", c.UnsubscribeNotFound("sub-1"), codes.NotFound, string(errorcodes.ConfigurationUnsubscribe.GrpcCode)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, ok := status.FromError(tc.err)
			require.True(t, ok)
			require.Equal(t, tc.wantCode, st.Code(), "gRPC status code")

			ei := configErrorInfo(t, tc.err)
			require.Equal(t, tc.wantReason, ei.GetReason(), "ErrorInfo.Reason")
			require.NotEmpty(t, ei.GetDomain(), "ErrorInfo.Domain should be populated")
		})
	}
}
