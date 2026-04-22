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

package errors_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	epb "google.golang.org/genproto/googleapis/rpc/errdetails"

	apierrors "github.com/dapr/dapr/pkg/api/errors"
)

func TestBindingInvokeOutputBinding(t *testing.T) {
	bindingName := "my-output-binding"
	cause := errors.New("connection refused")

	err := apierrors.Binding(bindingName).InvokeOutputBinding(cause)
	require.Error(t, err)

	// gRPC status code must be Internal
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())

	// Error message contains binding name and root cause
	assert.Contains(t, st.Message(), bindingName)
	assert.Contains(t, st.Message(), cause.Error())

	// Status details: ErrorInfo carries the metadata
	details := st.Details()
	require.NotEmpty(t, details)

	var errInfo *epb.ErrorInfo
	for _, d := range details {
		if ei, ok := d.(*epb.ErrorInfo); ok {
			errInfo = ei
			break
		}
	}
	require.NotNil(t, errInfo, "expected ErrorInfo in status details")
	assert.Equal(t, "DAPR_BINDING_INVOKE_OUTPUT_BINDING", errInfo.GetReason())
	assert.Equal(t, bindingName, errInfo.GetMetadata()["name"])
	assert.Equal(t, cause.Error(), errInfo.GetMetadata()["error"])
}
