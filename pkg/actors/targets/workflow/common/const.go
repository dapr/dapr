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
// completion whose scheduling durable history does not show yet. The sender
// retries with the result in hand; it matches by suffix across the router.
var ErrSchedulingNotDurable = errors.New("the task's scheduling is not yet durable")

// ErrSchedulingSuperseded is the orchestrator's refusal of an activity
// completion whose task the history shows scheduled under another execution,
// or passed without scheduling. A lagging read of a ContinueAsNew boundary
// looks the same, so the sender retries with the result in hand for its
// window and only then drops it; it matches by suffix across the router.
var ErrSchedulingSuperseded = errors.New("the task's scheduling was superseded")
