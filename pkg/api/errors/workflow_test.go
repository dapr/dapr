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
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/pkg/messages/errorcodes"
	kiterrors "github.com/dapr/kit/errors"
)

func TestWorkflowErrors(t *testing.T) {
	instanceID := "test-instance-id"
	workflowName := "test-workflow"
	mockErr := errors.New("mock error")

	t.Run("Get", func(t *testing.T) {
		err := Workflow(instanceID).Get(mockErr)
		validateError(t, err, codes.Internal, http.StatusInternalServerError, errorcodes.WorkflowGet.Code)
	})

	t.Run("Start", func(t *testing.T) {
		err := Workflow(instanceID).Start(workflowName, mockErr)
		validateError(t, err, codes.Internal, http.StatusInternalServerError, errorcodes.WorkflowStart.Code)
	})

	t.Run("Pause", func(t *testing.T) {
		err := Workflow(instanceID).Pause(mockErr)
		validateError(t, err, codes.Internal, http.StatusInternalServerError, errorcodes.WorkflowPause.Code)
	})

	t.Run("Resume", func(t *testing.T) {
		err := Workflow(instanceID).Resume(mockErr)
		validateError(t, err, codes.Internal, http.StatusInternalServerError, errorcodes.WorkflowResume.Code)
	})

	t.Run("Terminate", func(t *testing.T) {
		err := Workflow(instanceID).Terminate(mockErr)
		validateError(t, err, codes.Internal, http.StatusInternalServerError, errorcodes.WorkflowTerminate.Code)
	})

	t.Run("Purge", func(t *testing.T) {
		err := Workflow(instanceID).Purge(mockErr)
		validateError(t, err, codes.Internal, http.StatusInternalServerError, errorcodes.WorkflowPurge.Code)
	})

	t.Run("RaiseEvent", func(t *testing.T) {
		err := Workflow(instanceID).RaiseEvent(mockErr)
		validateError(t, err, codes.Internal, http.StatusInternalServerError, errorcodes.WorkflowRaiseEvent.Code)
	})

	t.Run("InstanceNotFound", func(t *testing.T) {
		err := Workflow(instanceID).InstanceNotFound()
		validateError(t, err, codes.NotFound, http.StatusNotFound, errorcodes.WorkflowInstanceIDNotFound.Code)
	})

	t.Run("InstanceExists", func(t *testing.T) {
		err := Workflow(instanceID).InstanceExists()
		validateError(t, err, codes.AlreadyExists, http.StatusConflict, errorcodes.WorkflowInstanceExists.Code)
	})

	t.Run("WorkflowNameMissing", func(t *testing.T) {
		err := WorkflowNameMissing()
		validateError(t, err, codes.InvalidArgument, http.StatusBadRequest, errorcodes.WorkflowNameMissing.Code)
	})

	t.Run("WorkflowEventNameMissing", func(t *testing.T) {
		err := WorkflowEventNameMissing()
		validateError(t, err, codes.InvalidArgument, http.StatusBadRequest, errorcodes.WorkflowEventNameMissing.Code)
	})

	t.Run("WorkflowInstanceIDMissing", func(t *testing.T) {
		err := WorkflowInstanceIDMissing()
		validateError(t, err, codes.InvalidArgument, http.StatusBadRequest, errorcodes.WorkflowInstanceIDProvidedMissing.Code)
	})

	t.Run("WorkflowInstanceIDInvalid", func(t *testing.T) {
		err := WorkflowInstanceIDInvalid(instanceID)
		validateError(t, err, codes.InvalidArgument, http.StatusBadRequest, errorcodes.WorkflowInstanceIDInvalid.Code)
	})

	t.Run("WorkflowInstanceIDTooLong", func(t *testing.T) {
		err := WorkflowInstanceIDTooLong(10)
		validateError(t, err, codes.InvalidArgument, http.StatusBadRequest, errorcodes.WorkflowInstanceIDTooLong.Code)
	})
}

func validateError(t *testing.T, err error, grpcCode codes.Code, httpCode int, errorCode string) {
	assert.Error(t, err)
	var kitErr *kiterrors.Error
	assert.True(t, errors.As(err, &kitErr))
	assert.Equal(t, grpcCode, kitErr.GRPCStatus().Code())
	assert.Equal(t, httpCode, kitErr.HTTPStatusCode())
	assert.Equal(t, errorCode, kitErr.ErrorCode())
}
