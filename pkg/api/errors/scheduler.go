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

	kiterrors "github.com/dapr/kit/errors"
)

const (
	CodePrefixScheduler               = "SCHEDULER_" // TODO(Cassie): move this to kit eventually
	InFixJob            ReasonSegment = "JOB_"
	InFixAppID          ReasonSegment = "APPID_"
	InFixGet            ReasonSegment = "GET_"
	InFixList           ReasonSegment = "LIST_"
	InFixDelete         ReasonSegment = "DELETE_"
	InFixSchedule       ReasonSegment = "SCHEDULE_"
	PostFixRepeats      ReasonSegment = "REPEATS"
	PostFixJob          ReasonSegment = "JOB"
	PostFixJobs         ReasonSegment = "JOBS"

	MsgScheduleJob = "failed to schedule job"
	MsgGetJob      = "failed to get job"
	MsgListJobs    = "failed to list jobs"
	MsgDeleteJob   = "failed to delete job"
)

func SchedulerURLName(metadata map[string]string) error {
	message := "Set the job name in the url only"
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		message,
		"",
	).
		WithErrorInfo(string(CodePrefixScheduler+InFixJob+PostFixName), metadata).
		Build()
}

func SchedulerScheduleJob(metadata map[string]string, err error) error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		MsgScheduleJob+" due to: "+err.Error(),
		"",
	).
		WithErrorInfo(string(CodePrefixScheduler+InFixSchedule+PostFixJob), metadata).
		Build()
}

func SchedulerGetJob(metadata map[string]string, err error) error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		MsgGetJob+" due to: "+err.Error(),
		"",
	).
		WithErrorInfo(string(CodePrefixScheduler+InFixGet+PostFixJob), metadata).
		Build()
}

func SchedulerListJobs(metadata map[string]string, err error) error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		MsgListJobs+" due to: "+err.Error(),
		"",
	).
		WithErrorInfo(string(CodePrefixScheduler+InFixList+PostFixJobs), metadata).
		Build()
}

func SchedulerDeleteJob(metadata map[string]string, err error) error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		MsgDeleteJob+" due to: "+err.Error(),
		"",
	).
		WithErrorInfo(string(CodePrefixScheduler+InFixDelete+PostFixJob), metadata).
		Build()
}
