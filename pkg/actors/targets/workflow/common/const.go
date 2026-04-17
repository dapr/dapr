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

const (
	ReminderPrefixActivityResult = "activity-result-"

	// ReminderPrefixXNSDispatch is the name prefix for caller-side reminders
	// that durably perform the cross-namespace service-invocation hop.
	ReminderPrefixXNSDispatch = "xns-dispatch-"

	// ReminderPrefixXNSExec is the name prefix for target-side reminders that
	// carry a cross-namespace dispatch into the local orchestrator/activity
	// actor via a regular local invocation.
	ReminderPrefixXNSExec = "xns-exec-"

	// ReminderPrefixXNSResult is the name prefix for target-side reminders
	// that durably ship a completion event back to the parent orchestrator's
	// sidecar via service invocation.
	ReminderPrefixXNSResult = "xns-result-"

	// ReminderPrefixXNSResultIn is the name prefix for parent-side reminders
	// created by DeliverWorkflowResultCrossNamespace that carry a cross-ns
	// result into the parent orchestrator's inbox. Split from
	// ReminderPrefixXNSResult so the firing handler on the parent side can
	// read the carried CrossNSResultRequest and validate the parent's
	// executionId before delivery (stale runs are dropped).
	ReminderPrefixXNSResultIn = "xns-result-in-"
)
