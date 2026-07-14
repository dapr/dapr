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

package messages

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	ErrorTypeAccessPolicyDenied    = "WorkflowAccessPolicyDenied"
	ErrorMessageAccessPolicyDenied = "access denied by workflow access policy"
	ErrorTypeAlreadyExists         = "WorkflowInstanceAlreadyExists"
)

func IsPermissionDenied(err error) bool {
	return HasGRPCStatusCode(err, codes.PermissionDenied)
}

func IsAlreadyExists(err error) bool {
	return HasGRPCStatusCode(err, codes.AlreadyExists)
}

// HasGRPCStatusCode checks whether the error (possibly wrapped) contains the
// given gRPC status code. Walks both single-error and multi-error chains.
func HasGRPCStatusCode(err error, code codes.Code) bool {
	if err == nil {
		return false
	}

	// Try direct gRPC status extraction.
	if st, ok := status.FromError(err); ok && st.Code() == code {
		return true
	}

	// Walk the full error chain. errors.As traverses both Unwrap() error
	// and Unwrap() []error chains (multi-error wrappers like errors.Join).
	var wrapped interface{ GRPCStatus() *status.Status }
	if errors.As(err, &wrapped) {
		if wrapped.GRPCStatus().Code() == code {
			return true
		}
	}

	return false
}

// GRPCStatusMessage returns the message of the innermost gRPC status in the
// error chain, so surfaced failure messages are stable regardless of how many
// layers wrapped the original status error.
func GRPCStatusMessage(err error) string {
	var gs interface{ GRPCStatus() *status.Status }
	if errors.As(err, &gs) {
		return gs.GRPCStatus().Message()
	}
	return err.Error()
}
