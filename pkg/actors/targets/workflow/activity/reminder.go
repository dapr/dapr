/*
Copyright 2025 The Dapr Authors
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

package activity

import (
	"context"
	"time"

	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	"github.com/dapr/durabletask-go/backend"
)

func (a *activity) createReminder(ctx context.Context, his *backend.HistoryEvent, dueTime time.Time) error {
	const reminderName = "run-activity"
	log.Debugf("Activity actor '%s||%s': creating reminder '%s' with dueTime=%s", a.actorType, a.actorID, reminderName, dueTime)

	anydata, err := anypb.New(his)
	if err != nil {
		return err
	}

	// The activity actor should always create reminders for its own actor type and ID
	return a.reminders.Create(ctx, &actorapi.CreateReminderRequest{
		ActorType: a.actorType,
		ActorID:   a.actorID,
		DueTime:   dueTime.Format(time.RFC3339),
		Name:      reminderName,
		// One shot, retry forever, every second.
		FailurePolicy: &commonv1pb.JobFailurePolicy{
			Policy: &commonv1pb.JobFailurePolicy_Constant{
				Constant: &commonv1pb.JobFailurePolicyConstant{
					Interval:   durationpb.New(time.Second),
					MaxRetries: nil,
				},
			},
		},
		Data: anydata,
	})
}
