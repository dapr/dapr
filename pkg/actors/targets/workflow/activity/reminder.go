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
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/anypb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/config"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/ptr"
)

type priority struct {
	level  schedulerv1pb.PriorityLevel
	weight uint32
}

var priorityLevels = map[string]schedulerv1pb.PriorityLevel{
	config.PriorityLevelCritical:   schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_CRITICAL,
	config.PriorityLevelHigh:       schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_HIGH,
	config.PriorityLevelNormal:     schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_NORMAL,
	config.PriorityLevelLow:        schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_LOW,
	config.PriorityLevelBackground: schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_BACKGROUND,
}

// priorityOf is this app's entry, else what the caller sent, else the default.
func (f *factory) priorityOf(md map[string]*internalsv1pb.ListStringValue, activityName *string) priority {
	level, weight := sentPriority(md)
	if f.receivedPriority != nil && activityName != nil {
		level, weight = f.receivedPriority(*activityName, level, weight)
	}
	return priority{level: priorityLevels[level], weight: weight}
}

// Unknown values stay unset.
func sentPriority(md map[string]*internalsv1pb.ListStringValue) (string, uint32) {
	var (
		level  string
		weight uint32
	)
	if v := md[todo.MetadataActivityPriorityLevel].GetValues(); len(v) > 0 {
		if _, ok := priorityLevels[v[0]]; ok {
			level = v[0]
		}
	}
	if v := md[todo.MetadataActivityFairnessWeight].GetValues(); len(v) > 0 {
		if w, err := strconv.ParseUint(v[0], 10, 32); err == nil && w >= config.MinFairnessWeight && w <= config.MaxFairnessWeight {
			weight = uint32(w)
		}
	}
	return level, weight
}

// activityReminderName is the constant name of the per-activity-actor
// execution reminder. One reminder per actor: retries and the drive-failure
// escalation collapse onto a single scheduler entry (overwrite-by-name).
const activityReminderName = todo.ActivityReminderName

// createActivityReminder lives on the factory (with an explicit actorID)
// rather than the *activity because the drive-failure escalation path may
// outlive the actor object (HaltAll recycles it).
func (f *factory) createActivityReminder(ctx context.Context, actorID string, invocation *protos.ActivityInvocation, dueTime time.Time, activityName *string, prio priority) error {
	// Clamp a past dueTime to now: the scheduler paces failure retries from
	// the scheduled time, so a stale dueTime replays the whole elapsed
	// backlog as a burst on every failed trigger.
	if now := time.Now(); dueTime.Before(now) {
		dueTime = now
	}

	var pull bool
	if f.dispatchPull != nil && activityName != nil {
		pull = f.dispatchPull(*activityName)
	}

	log.Debugf("Activity actor '%s||%s': creating reminder '%s' with dueTime=%s pull=%t", f.actorType, actorID, activityReminderName, dueTime, pull)

	anydata, err := anypb.New(invocation)
	if err != nil {
		return err
	}

	// The activity actor should always create reminders for its own actor type
	// and ID
	return common.CreateReminderWithRetry(ctx, f.reminders, &actorapi.CreateReminderRequest{
		ActorType: f.actorType,
		ActorID:   actorID,
		DueTime:   dueTime.Format(time.RFC3339Nano),
		Name:      activityReminderName,
		// One shot, retry forever, jittered interval.
		FailurePolicy:  common.RetryForeverPolicy(),
		Data:           anydata,
		ConcurrencyKey: activityName,
		Pull:           pull,
		PriorityLevel:  prio.level,
		FairnessWeight: prio.weight,
	})
}

func (f *factory) createWorkflowResultReminder(ctx context.Context, wfActorType, wfActorID, parentExecutionID string, result *backend.HistoryEvent) error {
	b := make([]byte, 6)
	_, err := io.ReadFull(rand.Reader, b)
	if err != nil {
		return fmt.Errorf("failed to generate reminder ID: %w", err)
	}

	reminderName := common.ActivityResultReminderName(base64.RawURLEncoding.EncodeToString(b), parentExecutionID)

	anydata, err := anypb.New(result)
	if err != nil {
		return err
	}

	return common.CreateReminderWithRetry(ctx, f.reminders, &actorapi.CreateReminderRequest{
		ActorType: wfActorType,
		ActorID:   wfActorID,
		DueTime:   activityResultDueTime(),
		Name:      reminderName,
		// One shot, retry forever, jittered interval.
		FailurePolicy: common.RetryForeverPolicy(),
		Data:          anydata,
		// The Scheduler only lets another app's result reminder be created,
		// never replaced; the name is random, so a retry of an unacked
		// create is the only AlreadyExists, and the retry helper treats it
		// as success.
		Overwrite: ptr.Of(false),
	})
}

// testActivityResultDelay holds a result reminder back for the configured
// duration so tests can observe the window between an activity settling its
// outcome and the parent receiving it. Zero (the default) is due now.
var testActivityResultDelay = sync.OnceValue(func() time.Duration {
	return common.EnvDurationOr("DAPR_WORKFLOW_TEST_ACTIVITY_RESULT_DELAY", 0)
})

func activityResultDueTime() string {
	delay := testActivityResultDelay()
	if delay <= 0 {
		return "0s"
	}
	log.Warnf("TEST INJECTION: holding the activity result reminder for %s", delay)
	return time.Now().Add(delay).UTC().Format(time.RFC3339Nano)
}
