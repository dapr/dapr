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

import "github.com/dapr/durabletask-go/backend"

// Key returns the inflight Map key for an activity invocation. It pairs the
// activity actor ID with the TaskExecutionId from the TaskScheduled event so
// retries of the same scheduled task share a cache entry while a new workflow
// run that re-uses the same instance ID (and therefore the same activity actor
// ID) gets a fresh entry.
func Key(actorID string, taskEvent *backend.HistoryEvent) string {
	if ts := taskEvent.GetTaskScheduled(); ts != nil {
		if id := ts.GetTaskExecutionId(); id != "" {
			return actorID + "::" + id
		}
	}
	return actorID
}
