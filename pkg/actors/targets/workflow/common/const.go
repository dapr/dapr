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

import "errors"

const (
	ReminderPrefixActivityResult = "activity-result-"
)

// ErrSchedulingNotDurable is the orchestrator's refusal of an activity
// completion whose scheduling the durable history does not show yet. A turn
// dispatches its activities before it saves, so a completion can reach
// admission ahead of its own TaskScheduled row: the sender must re-deliver
// rather than have the result dropped as unmatched. It crosses the router as
// a string, so callers match it by suffix.
var ErrSchedulingNotDurable = errors.New("the task's scheduling is not yet durable")
