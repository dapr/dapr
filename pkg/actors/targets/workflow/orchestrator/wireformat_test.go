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

package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
)

// Every value below is written into a durable artifact or sent to another
// daprd, so it is wire format. To change one: keep writing the old value,
// teach the reader the new one, add a row, and drop the old row only once
// every release that wrote it is outside the version skew window.
// pkg/runtime/wfengine/state/list/list.go hand-builds the workflow actor type
// string and is not covered here.
func TestWireFormatIdentifiers(t *testing.T) {
	t.Parallel()

	b := common.NewActorTypeBuilder("ns")

	for _, tc := range []struct{ got, want string }{
		{todo.ActivityReminderName, "run-activity"},
		{reminderPrefixStart, "start"},
		{reminderPrefixNewEvent, "new-event"},
		{reminderPrefixTimer, "timer-"},
		{reminderCascadeTerminate, "cascade-terminate"},
		{janitorReminderName, "new-event-janitor"},
		{retentionReminderName, "retention"},
		{common.ReminderPrefixActivityResult, "activity-result-"},
		{todo.CreateWorkflowInstanceMethod, "CreateWorkflowInstance"},
		{todo.AddWorkflowEventMethod, "AddWorkflowEvent"},
		{todo.PurgeWorkflowStateMethod, "PurgeWorkflowState"},
		{todo.RecursivePurgeWorkflowStateMethod, "RecursivePurgeWorkflowState"},
		{todo.WaitForRuntimeStatus, "WaitForRuntimeStatus"},
		{todo.ForkWorkflowHistory, "ForkWorkflowHistory"},
		{todo.RerunWorkflowInstance, "RerunWorkflowInstance"},
		{todo.ExecuteActivityMethod, "Execute"},
		{executor.MethodComplete, "Complete"},
		{executor.MethodCancel, "Cancel"},
		{executor.MethodClaim, "Claim"},
		{executor.MethodWatchComplete, "WatchComplete"},
		{todo.ActorTypePrefix, "dapr.internal."},
		{b.Workflow("app"), "dapr.internal.ns.app.workflow"},
		{b.Activity("app"), "dapr.internal.ns.app.activity"},
		{common.ActivityActorID("wf", 5), "wf::5::0"},
	} {
		assert.Equal(t, tc.want, tc.got,
			"wire-format identifier %q changed: artifacts written by released versions are stranded", tc.want)
	}
}
