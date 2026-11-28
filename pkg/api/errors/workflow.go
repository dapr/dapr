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

package errors

import (
	"fmt"
	"net/http"

	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/pkg/messages/errorcodes"
	kiterrors "github.com/dapr/kit/errors"
)

// Workflow* build the rich errors of the Workflows API. Messages and status
// codes match the legacy errors they replace in pkg/messages/predefined.go.
// When a name is set it is reported through ResourceInfo; validation errors
// that run before a name is known skip it.

func WorkflowStartFailed(name string, cause error) error {
	return buildWorkflowError(
		kiterrors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			fmt.Sprintf("error starting workflow '%s': %s", name, cause),
			errorcodes.WorkflowStart.Code,
			string(errorcodes.WorkflowStart.Category),
		),
		errorcodes.WorkflowStart,
		name,
	)
}

func WorkflowGetFailed(instanceID string, cause error) error {
	return buildWorkflowError(
		kiterrors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			fmt.Sprintf("error while getting workflow info on instance '%s': %s", instanceID, cause),
			errorcodes.WorkflowGet.Code,
			string(errorcodes.WorkflowGet.Category),
		),
		errorcodes.WorkflowGet,
		instanceID,
	)
}

func WorkflowNameMissing() error {
	return buildWorkflowError(
		kiterrors.NewBuilder(
			codes.InvalidArgument,
			http.StatusBadRequest,
			"workflow name is not configured",
			errorcodes.WorkflowNameMissing.Code,
			string(errorcodes.WorkflowNameMissing.Category),
		),
		errorcodes.WorkflowNameMissing,
		"",
	)
}

func WorkflowInstanceIDTooLong(max int) error {
	return buildWorkflowError(
		kiterrors.NewBuilder(
			codes.InvalidArgument,
			http.StatusBadRequest,
			fmt.Sprintf("workflow instance ID exceeds the max length of %d characters", max),
			errorcodes.WorkflowInstanceIDTooLong.Code,
			string(errorcodes.WorkflowInstanceIDTooLong.Category),
		),
		errorcodes.WorkflowInstanceIDTooLong,
		"",
	)
}

func WorkflowInstanceIDInvalid(instanceID string) error {
	return buildWorkflowError(
		kiterrors.NewBuilder(
			codes.InvalidArgument,
			http.StatusBadRequest,
			fmt.Sprintf("workflow instance ID '%s' is invalid: only alphanumeric and underscore characters are allowed", instanceID),
			errorcodes.WorkflowInstanceIDInvalid.Code,
			string(errorcodes.WorkflowInstanceIDInvalid.Category),
		),
		errorcodes.WorkflowInstanceIDInvalid,
		instanceID,
	)
}

func WorkflowInstanceNotFound(instanceID string) error {
	return buildWorkflowError(
		kiterrors.NewBuilder(
			codes.NotFound,
			http.StatusNotFound,
			fmt.Sprintf("unable to find workflow with the provided instance ID: %s", instanceID),
			errorcodes.WorkflowInstanceIDNotFound.Code,
			string(errorcodes.WorkflowInstanceIDNotFound.Category),
		),
		errorcodes.WorkflowInstanceIDNotFound,
		instanceID,
	)
}

func WorkflowInstanceIDMissing() error {
	return buildWorkflowError(
		kiterrors.NewBuilder(
			codes.InvalidArgument,
			http.StatusBadRequest,
			"no instance ID was provided",
			errorcodes.WorkflowInstanceIDProvidedMissing.Code,
			string(errorcodes.WorkflowInstanceIDProvidedMissing.Category),
		),
		errorcodes.WorkflowInstanceIDProvidedMissing,
		"",
	)
}

func WorkflowTerminateFailed(instanceID string, cause error) error {
	return buildWorkflowError(
		kiterrors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			fmt.Sprintf("error terminating workflow '%s': %s", instanceID, cause),
			errorcodes.WorkflowTerminate.Code,
			string(errorcodes.WorkflowTerminate.Category),
		),
		errorcodes.WorkflowTerminate,
		instanceID,
	)
}

func WorkflowEventNameMissing() error {
	return buildWorkflowError(
		kiterrors.NewBuilder(
			codes.InvalidArgument,
			http.StatusBadRequest,
			"missing workflow event name",
			errorcodes.WorkflowEventNameMissing.Code,
			string(errorcodes.WorkflowEventNameMissing.Category),
		),
		errorcodes.WorkflowEventNameMissing,
		"",
	)
}

func WorkflowRaiseEventFailed(instanceID string, cause error) error {
	return buildWorkflowError(
		kiterrors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			fmt.Sprintf("error raising event on workflow '%s': %s", instanceID, cause),
			errorcodes.WorkflowRaiseEvent.Code,
			string(errorcodes.WorkflowRaiseEvent.Category),
		),
		errorcodes.WorkflowRaiseEvent,
		instanceID,
	)
}

func WorkflowPauseFailed(instanceID string, cause error) error {
	return buildWorkflowError(
		kiterrors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			fmt.Sprintf("error pausing workflow %s: %s", instanceID, cause),
			errorcodes.WorkflowPause.Code,
			string(errorcodes.WorkflowPause.Category),
		),
		errorcodes.WorkflowPause,
		instanceID,
	)
}

func WorkflowResumeFailed(instanceID string, cause error) error {
	return buildWorkflowError(
		kiterrors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			fmt.Sprintf("error resuming workflow %s: %s", instanceID, cause),
			errorcodes.WorkflowResume.Code,
			string(errorcodes.WorkflowResume.Category),
		),
		errorcodes.WorkflowResume,
		instanceID,
	)
}

func WorkflowPurgeFailed(instanceID string, cause error) error {
	return buildWorkflowError(
		kiterrors.NewBuilder(
			codes.Internal,
			http.StatusInternalServerError,
			fmt.Sprintf("error purging workflow %s: %s", instanceID, cause),
			errorcodes.WorkflowPurge.Code,
			string(errorcodes.WorkflowPurge.Category),
		),
		errorcodes.WorkflowPurge,
		instanceID,
	)
}

func WorkflowAppIDInvalid(appID string) error {
	return buildWorkflowError(
		kiterrors.NewBuilder(
			codes.InvalidArgument,
			http.StatusBadRequest,
			fmt.Sprintf("workflow app ID '%s' is invalid: only alphanumeric, dash and underscore characters are allowed", appID),
			errorcodes.WorkflowAppIDInvalid.Code,
			string(errorcodes.WorkflowAppIDInvalid.Category),
		),
		errorcodes.WorkflowAppIDInvalid,
		appID,
	)
}

func buildWorkflowError(err *kiterrors.ErrorBuilder, code errorcodes.ErrorCode, name string) error {
	if name != "" {
		err = err.WithResourceInfo("workflow", name, "", "")
	}
	return err.
		WithErrorInfo(code.GrpcCode, nil).
		Build()
}
