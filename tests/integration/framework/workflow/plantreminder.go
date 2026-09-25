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

package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/durabletask-go/api/protos"
)

// PlantReminder schedules a due-now reminder named name against the workflow
// actor for instanceID, carrying ev as its data and the retry-forever failure
// policy real activity-result reminders are created with. It delivers an event
// the runtime would not produce on its own, which is how a test reaches the
// reminder-driven admission path directly. The scheduler client is passed in
// because a test running with mTLS must use its own app identity.
func PlantReminder(t *testing.T, ctx context.Context, client schedulerv1pb.SchedulerClient, appID, instanceID, name string, ev *protos.HistoryEvent) {
	t.Helper()

	data, err := anypb.New(ev)
	require.NoError(t, err)

	dueTime := time.Now().Format(time.RFC3339)
	_, err = client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name: name,
		Job:  &schedulerv1pb.Job{DueTime: &dueTime, Data: data, FailurePolicy: common.RetryForeverPolicy()},
		Metadata: &schedulerv1pb.JobMetadata{
			Namespace: "default",
			AppId:     appID,
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{
						Type: "dapr.internal.default." + appID + ".workflow",
						Id:   instanceID,
					},
				},
			},
		},
	})
	require.NoError(t, err)
}
