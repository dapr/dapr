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

package pubsub

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
)

// TestNewPublishResiliencyError asserts the broker's gRPC status code is kept
// as-is for gRPC publishes (matched against gRPCStatusCodes) and mapped to its
// HTTP equivalent for HTTP publishes (matched against httpStatusCodes).
func TestNewPublishResiliencyError(t *testing.T) {
	baseErr := errors.New("broker boom")

	tests := []struct {
		name     string
		mode     TransportMode
		code     codes.Code
		wantCode int32
	}{
		// gRPC transport keeps the native gRPC code (0-16 -> gRPCStatusCodes).
		{"grpc retriable keeps Unavailable", TransportModeGRPC, codes.Unavailable, int32(codes.Unavailable)},
		{"grpc terminal keeps FailedPrecondition", TransportModeGRPC, codes.FailedPrecondition, int32(codes.FailedPrecondition)},
		// HTTP transport maps the gRPC code to its HTTP status (100-599 -> httpStatusCodes).
		{"http maps Unavailable to 503", TransportModeHTTP, codes.Unavailable, 503},
		{"http maps FailedPrecondition to 400", TransportModeHTTP, codes.FailedPrecondition, 400},
		{"http maps ResourceExhausted to 429", TransportModeHTTP, codes.ResourceExhausted, 429},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cErr := NewPublishResiliencyError(tt.mode, tt.code, baseErr)
			assert.Equal(t, tt.wantCode, cErr.StatusCode)
			// The original error is preserved for callers/logging.
			require.ErrorIs(t, cErr, baseErr)
		})
	}
}
