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

package inflight

import (
	"strconv"

	"github.com/dapr/durabletask-go/backend"
)

// Key returns the inflight Map key for an activity invocation. It pairs the
// activity actor ID with the TaskExecutionId from the TaskScheduled event so
// retries of the same scheduled task share a cache entry while a new workflow
// run that re-uses the same instance ID (and therefore the same activity actor
// ID) gets a fresh entry.
//
// SDKs that predate TaskExecutionId leave it empty; the event's timestamp is
// used instead. The timestamp is part of the persisted event carried by the
// run-activity reminder, so retries of the same scheduling see the same
// value, while a later scheduling that maps to the same activity actor (a
// continued-as-new generation re-using the task ID) carries a fresh timestamp
// and gets a fresh entry. Without a discriminator, a continue-as-new loop
// faster than the inflight cache TTL would join its previous generation's
// completed call as a follower and never execute (the monitor pattern).
func Key(actorID string, taskEvent *backend.HistoryEvent) string {
	if ts := taskEvent.GetTaskScheduled(); ts != nil {
		if id := ts.GetTaskExecutionId(); id != "" {
			return actorID + "::" + id
		}
	}
	if ts := taskEvent.GetTimestamp(); ts != nil {
		return actorID + "::" + strconv.FormatInt(ts.GetSeconds(), 10) + "." + strconv.FormatInt(int64(ts.GetNanos()), 10)
	}
	return actorID
}
