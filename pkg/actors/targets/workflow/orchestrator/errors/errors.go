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

package errors

import "fmt"

// DetachedSpawnDeniedError signals that a detached workflow spawn was
// rejected by the target app's WorkflowAccessPolicy. The dispatch path
// returns this so the orchestrator run loop can fail the parent terminally
// instead of silently dropping the action — the orchestrator function has
// already returned synchronously from ScheduleNewWorkflow with a non-empty
// instance ID, so the operator's only signal that something went wrong is
// the parent's terminal status.
type DetachedSpawnDeniedError struct {
	InstanceID  string
	TargetAppID string
	Cause       error
}

func (e *DetachedSpawnDeniedError) Error() string {
	return fmt.Sprintf("detached workflow spawn '%s' denied by access policy on app '%s': %v", e.InstanceID, e.TargetAppID, e.Cause)
}

func (e *DetachedSpawnDeniedError) Unwrap() error { return e.Cause }
