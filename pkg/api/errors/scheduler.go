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
	"net/http"

	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/pkg/messages/errorcodes"
	kiterrors "github.com/dapr/kit/errors"
)

func SchedulerURLName(metadata map[string]string) error {
	message := "Set the job name in the url only"
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		message,
		"",
		string(errorcodes.SchedulerJobName.Category),
	).
		WithErrorInfo(errorcodes.SchedulerJobName.Code, metadata).
		Build()
}

func SchedulerScheduleJob(metadata map[string]string, err error) error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		"failed to schedule job due to: "+err.Error(),
		"",
		string(errorcodes.SchedulerScheduleJob.Category),
	).
		WithErrorInfo(errorcodes.SchedulerScheduleJob.Code, metadata).
		Build()
}

func SchedulerGetJob(metadata map[string]string, err error) error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		"failed to get job due to: "+err.Error(),
		"",
		string(errorcodes.SchedulerGetJob.Category),
	).
		WithErrorInfo(errorcodes.SchedulerGetJob.Code, metadata).
		Build()
}

func SchedulerListJobs(metadata map[string]string, err error) error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		"failed to list jobs due to: "+err.Error(),
		"",
		string(errorcodes.SchedulerListJobs.Category),
	).
		WithErrorInfo(errorcodes.SchedulerListJobs.Code, metadata).
		Build()
}

func SchedulerDeleteJob(metadata map[string]string, err error) error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		"failed to delete job due to: "+err.Error(),
		"",
		string(errorcodes.SchedulerDeleteJob.Category),
	).
		WithErrorInfo(errorcodes.SchedulerDeleteJob.Code, metadata).
		Build()
}
