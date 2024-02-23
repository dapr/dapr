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
	CodePrefixScheduler = "SCHEDULER_" // TODO(Cassie): move this to kit eventually
	InFixJob            = "JOB_"
	InFixSchedule       = "SCHEDULE_"
	PostFixRepeats      = "REPEATS"
)

func SchedulerURLName(metadata map[string]string) error {
	message := "Set the job name in the url only"
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		message,
		"",
	).
		WithErrorInfo(CodePrefixScheduler+InFixJob+PostFixName, metadata).
		Build()
}
