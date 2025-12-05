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

package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
)

func (o *orchestrator) createWorkflowReminder(ctx context.Context, namePrefix string, data proto.Message, start time.Time, targetAppID string) (string, error) {
	actorType := o.actorTypeBuilder.Workflow(targetAppID)
	return o.createReminderWithType(ctx, namePrefix, data, start, actorType)
}

func (o *orchestrator) createRetentionReminder(ctx context.Context, namePrefix string, start time.Time) (string, error) {
	return o.createReminderWithType(ctx, namePrefix, nil, start, o.retentionActorType)
}

func (o *orchestrator) createReminderWithType(ctx context.Context, namePrefix string, data proto.Message, start time.Time, actorType string) (string, error) {
	b := make([]byte, 6)
	_, err := io.ReadFull(rand.Reader, b)
	if err != nil {
		return "", fmt.Errorf("failed to generate reminder ID: %w", err)
	}

	dueTime := start.UTC().Format(time.RFC3339)
	reminderName := namePrefix + "-" + base64.RawURLEncoding.EncodeToString(b)

	var adata *anypb.Any
	if data != nil {
		adata, err = anypb.New(data)
		if err != nil {
			return "", err
		}
	}

	log.Debugf("Workflow actor '%s||%s': creating '%s' reminder with DueTime = '%s'", actorType, o.actorID, reminderName, dueTime)

	return reminderName, o.reminders.Create(ctx, &actorapi.CreateReminderRequest{
		ActorType: actorType,
		ActorID:   o.actorID,
		Data:      adata,
		DueTime:   dueTime,
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
	})
}
