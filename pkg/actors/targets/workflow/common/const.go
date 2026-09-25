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

package common

import (
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	ReminderPrefixActivityResult = "activity-result-"
)

// ErrSchedulingNotDurable is the orchestrator's refusal of an activity
// completion whose scheduling durable history does not show yet. The sender
// retries with the result in hand; it matches by suffix across the router.
var ErrSchedulingNotDurable = errors.New("the task's scheduling is not yet durable")

// ErrSchedulingSuperseded is the orchestrator's refusal of an activity
// completion whose task the history shows scheduled under another execution,
// or passed without scheduling. A lagging read of a ContinueAsNew boundary
// looks the same, so the sender retries with the result in hand for its
// window and only then drops it; it matches by suffix across the router.
var ErrSchedulingSuperseded = errors.New("the task's scheduling was superseded")

// defaultPublishRetryWindow bounds how long a refused activity result is
// retried with the result in hand before the refusal reaches the reminder
// chain, and how long the reminder chain itself keeps refiring one. It sits
// inside activity.detachedPublishTimeout.
const defaultPublishRetryWindow = 20 * time.Second

// PublishRetryWindow resolves the in-hand retry window once per process. The
// orchestrator bounds a refusal by the same window the sender spends on it,
// so the window is spent exactly once wherever it is spent.
var PublishRetryWindow = sync.OnceValue(func() time.Duration {
	return EnvDurationOr("DAPR_WORKFLOW_TEST_ACTIVITY_PUBLISH_RETRY_WINDOW", defaultPublishRetryWindow)
})

// IsSchedulingRefusal reports whether err is one of the orchestrator's two
// scheduling refusals. They cross the router as strings, so they match by
// suffix.
func IsSchedulingRefusal(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.HasSuffix(msg, ErrSchedulingNotDurable.Error()) ||
		strings.HasSuffix(msg, ErrSchedulingSuperseded.Error())
}
