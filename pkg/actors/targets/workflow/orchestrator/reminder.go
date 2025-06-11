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

	actorapi "github.com/dapr/dapr/pkg/actors/api"
)

func (o *orchestrator) createReminder(ctx context.Context, namePrefix string, data proto.Message, delay time.Duration) (string, error) {
	b := make([]byte, 6)
	_, err := io.ReadFull(rand.Reader, b)
	if err != nil {
		return "", fmt.Errorf("failed to generate reminder ID: %w", err)
	}

	reminderName := namePrefix + "-" + base64.RawURLEncoding.EncodeToString(b)
	log.Debugf("Workflow actor '%s||%s': creating '%s' reminder with DueTime = '%s'", o.activityActorType, o.actorID, reminderName, delay)

	var period string
	var oneshot bool
	if o.schedulerReminders {
		oneshot = true
	} else {
		period = o.reminderInterval.String()
	}

	var adata *anypb.Any
	if data != nil {
		adata, err = anypb.New(data)
		if err != nil {
			return "", err
		}
	}

	return reminderName, o.reminders.Create(ctx, &actorapi.CreateReminderRequest{
		ActorType: o.actorType,
		ActorID:   o.actorID,
		Data:      adata,
		DueTime:   delay.String(),
		Name:      reminderName,
		Period:    period,
		IsOneShot: oneshot,
	})
}
