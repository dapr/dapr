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
	"net/http"

	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/pkg/messages/errorcodes"
	"github.com/dapr/kit/errors"
)

type WorkflowError struct{}

func Workflow() *WorkflowError {
	return &WorkflowError{}
}

func (w *WorkflowError) InstanceIDProvidedMissing() error {
	return errors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		"no instance ID was provided",
		errorcodes.WorkflowInstanceIDProvidedMissing.Code,
		string(errorcodes.WorkflowInstanceIDProvidedMissing.Category),
	).
		WithErrorInfo(errorcodes.WorkflowInstanceIDProvidedMissing.GrpcCode, nil).
		Build()
}

func (w *WorkflowError) InstanceIDTooLong(maxLen int) error {
	return errors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		fmt.Sprintf("workflow instance ID exceeds the max length of %d characters", maxLen),
		errorcodes.WorkflowInstanceIDTooLong.Code,
		string(errorcodes.WorkflowInstanceIDTooLong.Category),
	).
		WithErrorInfo(errorcodes.WorkflowInstanceIDTooLong.GrpcCode, nil).
		Build()
}

func (w *WorkflowError) InstanceIDInvalid(instanceID string) error {
	return errors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		fmt.Sprintf("workflow instance ID '%s' is invalid: only alphanumeric and underscore characters are allowed", instanceID),
		errorcodes.WorkflowInstanceIDInvalid.Code,
		string(errorcodes.WorkflowInstanceIDInvalid.Category),
	).
		WithErrorInfo(errorcodes.WorkflowInstanceIDInvalid.GrpcCode, nil).
		Build()
}

func (w *WorkflowError) InstanceNotFound(instanceID string) error {
	return errors.NewBuilder(
		codes.NotFound,
		http.StatusNotFound,
		fmt.Sprintf("unable to find workflow with the provided instance ID: %s", instanceID),
		errorcodes.WorkflowInstanceIDNotFound.Code,
		string(errorcodes.WorkflowInstanceIDNotFound.Category),
	).
		WithErrorInfo(errorcodes.WorkflowInstanceIDNotFound.GrpcCode, nil).
		Build()
}

func (w *WorkflowError) NameMissing() error {
	return errors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		"workflow name is not configured",
		errorcodes.WorkflowNameMissing.Code,
		string(errorcodes.WorkflowNameMissing.Category),
	).
		WithErrorInfo(errorcodes.WorkflowNameMissing.GrpcCode, nil).
		Build()
}

func (w *WorkflowError) EventNameMissing() error {
	return errors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		"missing workflow event name",
		errorcodes.WorkflowEventNameMissing.Code,
		string(errorcodes.WorkflowEventNameMissing.Category),
	).
		WithErrorInfo(errorcodes.WorkflowEventNameMissing.GrpcCode, nil).
		Build()
}

func (w *WorkflowError) GetFailed(instanceID string, err error) error {
	return errors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("error while getting workflow info on instance '%s': %s", instanceID, err),
		errorcodes.WorkflowGet.Code,
		string(errorcodes.WorkflowGet.Category),
	).
		WithErrorInfo(errorcodes.WorkflowGet.GrpcCode, nil).
		Build()
}

func (w *WorkflowError) StartFailed(workflowName string, err error) error {
	return errors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("error starting workflow '%s': %s", workflowName, err),
		errorcodes.WorkflowStart.Code,
		string(errorcodes.WorkflowStart.Category),
	).
		WithErrorInfo(errorcodes.WorkflowStart.GrpcCode, nil).
		Build()
}

func (w *WorkflowError) TerminateFailed(instanceID string, err error) error {
	return errors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("error terminating workflow '%s': %s", instanceID, err),
		errorcodes.WorkflowTerminate.Code,
		string(errorcodes.WorkflowTerminate.Category),
	).
		WithErrorInfo(errorcodes.WorkflowTerminate.GrpcCode, nil).
		Build()
}

func (w *WorkflowError) RaiseEventFailed(instanceID string, err error) error {
	return errors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("error raising event on workflow '%s': %s", instanceID, err),
		errorcodes.WorkflowRaiseEvent.Code,
		string(errorcodes.WorkflowRaiseEvent.Category),
	).
		WithErrorInfo(errorcodes.WorkflowRaiseEvent.GrpcCode, nil).
		Build()
}

func (w *WorkflowError) PauseFailed(instanceID string, err error) error {
	return errors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("error pausing workflow %s: %s", instanceID, err),
		errorcodes.WorkflowPause.Code,
		string(errorcodes.WorkflowPause.Category),
	).
		WithErrorInfo(errorcodes.WorkflowPause.GrpcCode, nil).
		Build()
}

func (w *WorkflowError) ResumeFailed(instanceID string, err error) error {
	return errors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("error resuming workflow %s: %s", instanceID, err),
		errorcodes.WorkflowResume.Code,
		string(errorcodes.WorkflowResume.Category),
	).
		WithErrorInfo(errorcodes.WorkflowResume.GrpcCode, nil).
		Build()
}

func (w *WorkflowError) PurgeFailed(instanceID string, err error) error {
	return errors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("error purging workflow %s: %s", instanceID, err),
		errorcodes.WorkflowPurge.Code,
		string(errorcodes.WorkflowPurge.Category),
	).
		WithErrorInfo(errorcodes.WorkflowPurge.GrpcCode, nil).
		Build()
}
