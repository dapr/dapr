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

package orchestrator

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsPermissionDenied(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		assert.False(t, isPermissionDenied(nil))
	})

	t.Run("PermissionDenied gRPC error", func(t *testing.T) {
		err := status.Error(codes.PermissionDenied, "access denied by workflow access policy")
		assert.True(t, isPermissionDenied(err))
	})

	t.Run("PermissionDenied with requires marker", func(t *testing.T) {
		err := status.Error(codes.PermissionDenied, "access denied by workflow access policy [requires]")
		assert.True(t, isPermissionDenied(err))
	})

	t.Run("non-PermissionDenied gRPC error", func(t *testing.T) {
		err := status.Error(codes.NotFound, "not found")
		assert.False(t, isPermissionDenied(err))
	})

	t.Run("plain error is not denied", func(t *testing.T) {
		assert.False(t, isPermissionDenied(errors.New("some other error")))
	})
}

func TestIsPermissionDeniedRequiresUnmet(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		assert.False(t, isPermissionDeniedRequiresUnmet(nil))
	})

	t.Run("plain PermissionDenied (no marker)", func(t *testing.T) {
		err := status.Error(codes.PermissionDenied, "access denied by workflow access policy")
		assert.False(t, isPermissionDeniedRequiresUnmet(err))
	})

	t.Run("PermissionDenied with requires marker", func(t *testing.T) {
		err := status.Error(codes.PermissionDenied, "access denied by workflow access policy [requires]")
		assert.True(t, isPermissionDeniedRequiresUnmet(err))
	})

	t.Run("requires marker on non-PermissionDenied is ignored", func(t *testing.T) {
		err := status.Error(codes.Internal, "[requires] some unrelated message")
		assert.False(t, isPermissionDeniedRequiresUnmet(err))
	})
}

func TestACLFailureType(t *testing.T) {
	t.Run("plain deny = WorkflowAccessPolicyDenied", func(t *testing.T) {
		err := status.Error(codes.PermissionDenied, "access denied by workflow access policy")
		errType, errMsg := aclFailureType(err)
		assert.Equal(t, "WorkflowAccessPolicyDenied", errType)
		assert.Equal(t, "access denied by workflow access policy", errMsg)
	})

	t.Run("requires-unmet = WorkflowAccessPolicyRequiresUnmet", func(t *testing.T) {
		err := status.Error(codes.PermissionDenied, "access denied by workflow access policy [requires]")
		errType, errMsg := aclFailureType(err)
		assert.Equal(t, "WorkflowAccessPolicyRequiresUnmet", errType)
		assert.Equal(t, "access denied by workflow access policy: required history not satisfied", errMsg)
	})
}
