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
	"strconv"

	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/pkg/messages/errorcodes"
	kiterrors "github.com/dapr/kit/errors"
)

const (
	workflowResourceType = "workflow"
)

type WorkflowError struct {
	instanceID string
}

func workflowErrorReason(code errorcodes.ErrorCode) string {
	if code.GrpcCode != "" {
		return code.GrpcCode
	}
	return code.Code
}

func Workflow(instanceID string) *WorkflowError {
	return &WorkflowError{
		instanceID: instanceID,
	}
}

func (w *WorkflowError) Get(err error) error {
	message := fmt.Sprintf("error while getting workflow info on instance '%s': %s", w.instanceID, err)
	return w.build(
		codes.Internal,
		http.StatusInternalServerError,
		message,
		errorcodes.WorkflowGet,
		map[string]string{"error": err.Error()},
	)
}

func (w *WorkflowError) Start(workflowName string, err error) error {
	message := fmt.Sprintf("error starting workflow '%s': %s", workflowName, err)
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		message,
		errorcodes.WorkflowStart.Code,
		string(errorcodes.WorkflowStart.Category),
	).
		WithResourceInfo(workflowResourceType, workflowName, "", "").
		WithErrorInfo(workflowErrorReason(errorcodes.WorkflowStart), map[string]string{
			"instanceId":   w.instanceID,
			"workflowName": workflowName,
			"error":        err.Error(),
		}).
		Build()
}

func (w *WorkflowError) Pause(err error) error {
	message := fmt.Sprintf("error pausing workflow %s: %s", w.instanceID, err)
	return w.build(
		codes.Internal,
		http.StatusInternalServerError,
		message,
		errorcodes.WorkflowPause,
		map[string]string{"error": err.Error()},
	)
}

func (w *WorkflowError) Resume(err error) error {
	message := fmt.Sprintf("error resuming workflow %s: %s", w.instanceID, err)
	return w.build(
		codes.Internal,
		http.StatusInternalServerError,
		message,
		errorcodes.WorkflowResume,
		map[string]string{"error": err.Error()},
	)
}

func (w *WorkflowError) Terminate(err error) error {
	message := fmt.Sprintf("error terminating workflow '%s': %s", w.instanceID, err)
	return w.build(
		codes.Internal,
		http.StatusInternalServerError,
		message,
		errorcodes.WorkflowTerminate,
		map[string]string{"error": err.Error()},
	)
}

func (w *WorkflowError) Purge(err error) error {
	message := fmt.Sprintf("error purging workflow %s: %s", w.instanceID, err)
	return w.build(
		codes.Internal,
		http.StatusInternalServerError,
		message,
		errorcodes.WorkflowPurge,
		map[string]string{"error": err.Error()},
	)
}

func (w *WorkflowError) RaiseEvent(err error) error {
	message := fmt.Sprintf("error raising event on workflow '%s': %s", w.instanceID, err)
	return w.build(
		codes.Internal,
		http.StatusInternalServerError,
		message,
		errorcodes.WorkflowRaiseEvent,
		map[string]string{"error": err.Error()},
	)
}

func (w *WorkflowError) InstanceNotFound() error {
	message := "unable to find workflow with the provided instance ID: " + w.instanceID
	return w.build(
		codes.NotFound,
		http.StatusNotFound,
		message,
		errorcodes.WorkflowInstanceIDNotFound,
		nil,
	)
}

func (w *WorkflowError) build(grpcCode codes.Code, httpCode int, msg string, errCode errorcodes.ErrorCode, metadata map[string]string) error {
	return kiterrors.NewBuilder(
		grpcCode,
		httpCode,
		msg,
		errCode.Code,
		string(errCode.Category),
	).
		WithResourceInfo(workflowResourceType, w.instanceID, "", "").
		WithErrorInfo(workflowErrorReason(errCode), metadata).
		Build()
}

// WorkflowNameMissing returns an error when the workflow name is not configured.
func WorkflowNameMissing() error {
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		"workflow name is not configured",
		errorcodes.WorkflowNameMissing.Code,
		string(errorcodes.WorkflowNameMissing.Category),
	).
		WithErrorInfo(workflowErrorReason(errorcodes.WorkflowNameMissing), nil).
		Build()
}

// WorkflowEventNameMissing returns an error when the workflow event name is missing.
func WorkflowEventNameMissing() error {
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		"missing workflow event name",
		errorcodes.WorkflowEventNameMissing.Code,
		string(errorcodes.WorkflowEventNameMissing.Category),
	).
		WithErrorInfo(workflowErrorReason(errorcodes.WorkflowEventNameMissing), nil).
		Build()
}

// WorkflowInstanceIDMissing returns an error when the workflow instance ID is missing.
func WorkflowInstanceIDMissing() error {
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		"no instance ID was provided",
		errorcodes.WorkflowInstanceIDProvidedMissing.Code,
		string(errorcodes.WorkflowInstanceIDProvidedMissing.Category),
	).
		WithErrorInfo(workflowErrorReason(errorcodes.WorkflowInstanceIDProvidedMissing), nil).
		Build()
}

// WorkflowInstanceIDInvalid returns an error when the workflow instance ID contains invalid characters.
func WorkflowInstanceIDInvalid(instanceID string) error {
	message := fmt.Sprintf("workflow instance ID '%s' is invalid: only alphanumeric and underscore characters are allowed", instanceID)
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		message,
		errorcodes.WorkflowInstanceIDInvalid.Code,
		string(errorcodes.WorkflowInstanceIDInvalid.Category),
	).
		WithErrorInfo(workflowErrorReason(errorcodes.WorkflowInstanceIDInvalid), map[string]string{
			"instanceId": instanceID,
		}).
		Build()
}

// WorkflowInstanceIDTooLong returns an error when the workflow instance ID exceeds the max length.
func WorkflowInstanceIDTooLong(maxLength int) error {
	message := fmt.Sprintf("workflow instance ID exceeds the max length of %d characters", maxLength)
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		message,
		errorcodes.WorkflowInstanceIDTooLong.Code,
		string(errorcodes.WorkflowInstanceIDTooLong.Category),
	).
		WithErrorInfo(workflowErrorReason(errorcodes.WorkflowInstanceIDTooLong), map[string]string{
			"maxLength": strconv.Itoa(maxLength),
		}).
		Build()
}
