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

// Package statefulhistory contains integration tests for the stateful-history
// (delta) work-item optimization, where a worker that advertises
// WORKER_CAPABILITY_STATEFUL_HISTORY retains an instance's committed history
// between turns so the sidecar sends only the new events. The tests assert both
// correctness (the reconstructed history yields the same result as a full-history
// run) and that deltas actually flow on the wire, across reconnects, multiple
// sidecars, and multiple competing workers.
package statefulhistory

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

// historySignature projects a history into an ordered, volatile-field-free
// signature: one entry per event capturing its type and deterministic payload,
// ignoring timestamps, instance/execution IDs, and trace contexts. Two runs of
// the same deterministic workflow must produce identical signatures regardless of
// how their histories were delivered (full sends vs deltas vs GetInstanceHistory
// recovery), so it is used to diff an optimized run against a baseline run.
func historySignature(events []*protos.HistoryEvent) []string {
	sig := make([]string, 0, len(events))
	for _, e := range events {
		typ := fmt.Sprintf("%T", e.GetEventType())
		switch {
		case e.GetExecutionStarted() != nil:
			es := e.GetExecutionStarted()
			sig = append(sig, fmt.Sprintf("%s name=%s input=%s", typ, es.GetName(), es.GetInput().GetValue()))
		case e.GetExecutionCompleted() != nil:
			ec := e.GetExecutionCompleted()
			sig = append(sig, fmt.Sprintf("%s status=%s result=%s", typ, ec.GetWorkflowStatus(), ec.GetResult().GetValue()))
		case e.GetTaskScheduled() != nil:
			ts := e.GetTaskScheduled()
			sig = append(sig, fmt.Sprintf("%s name=%s input=%s", typ, ts.GetName(), ts.GetInput().GetValue()))
		case e.GetTaskCompleted() != nil:
			sig = append(sig, fmt.Sprintf("%s result=%s", typ, e.GetTaskCompleted().GetResult().GetValue()))
		default:
			sig = append(sig, typ)
		}
	}
	return sig
}

// assertAccumulateHistory verifies that the persisted history of an
// accumulate-style instance (accumulate(n) or accumulateThenWait(n)) is exactly
// what a correct run must produce: one ExecutionStarted, n AddOne activities
// scheduled with inputs 0..n-1 and completed with results 1..n in order, and one
// successful ExecutionCompleted whose output is n. This pins the *content* of the
// reconstructed history (whether it was rebuilt from deltas or recovered via
// GetInstanceHistory), not just the final output value.
func assertAccumulateHistory(t *testing.T, events []*protos.HistoryEvent, n int) {
	t.Helper()

	var (
		execStarted, execCompleted int
		scheduledInputs            []string
		completedResults           []string
	)
	for _, e := range events {
		switch {
		case e.GetExecutionStarted() != nil:
			execStarted++
		case e.GetExecutionCompleted() != nil:
			execCompleted++
			assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
				e.GetExecutionCompleted().GetWorkflowStatus(), "workflow must complete successfully")
			assert.Equal(t, strconv.Itoa(n), e.GetExecutionCompleted().GetResult().GetValue(), "final output")
		case e.GetTaskScheduled() != nil:
			assert.Equal(t, "AddOne", e.GetTaskScheduled().GetName())
			scheduledInputs = append(scheduledInputs, e.GetTaskScheduled().GetInput().GetValue())
		case e.GetTaskCompleted() != nil:
			completedResults = append(completedResults, e.GetTaskCompleted().GetResult().GetValue())
		}
	}

	assert.Equal(t, 1, execStarted, "exactly one ExecutionStarted")
	assert.Equal(t, 1, execCompleted, "exactly one ExecutionCompleted")

	expInputs := make([]string, n)
	expResults := make([]string, n)
	for i := range n {
		expInputs[i] = strconv.Itoa(i)
		expResults[i] = strconv.Itoa(i + 1)
	}
	assert.Equal(t, expInputs, scheduledInputs, "activity inputs, in order")
	assert.Equal(t, expResults, completedResults, "activity results, in order")
}

// accumulate returns a workflow that calls the AddOne activity n times
// sequentially, accumulating the result. Each sequential await forces a distinct
// turn with a longer committed history, so turns after the first are eligible to
// be sent as deltas.
func accumulate(n int) func(*task.WorkflowContext) (any, error) {
	return func(ctx *task.WorkflowContext) (any, error) {
		total := 0
		for i := 0; i < n; i++ {
			var got int
			if err := ctx.CallActivity("AddOne", task.WithActivityInput(total)).Await(&got); err != nil {
				return nil, err
			}
			total = got
		}
		return total, nil
	}
}

func addOne(ctx task.ActivityContext) (any, error) {
	var n int
	if err := ctx.GetInput(&n); err != nil {
		return nil, err
	}
	return n + 1, nil
}

// waitAccumulate returns a workflow that, before accumulating n AddOne calls,
// first blocks on an external event named "go". This lets a test build up some
// committed history and then pause the instance at a known point (e.g. to drop
// and reconnect the worker) before driving it to completion by raising the event.
func waitAccumulate(n int) func(*task.WorkflowContext) (any, error) {
	return func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.WaitForSingleEvent("go", -1).Await(nil); err != nil {
			return nil, err
		}
		total := 0
		for i := 0; i < n; i++ {
			var got int
			if err := ctx.CallActivity("AddOne", task.WithActivityInput(total)).Await(&got); err != nil {
				return nil, err
			}
			total = got
		}
		return total, nil
	}
}

// accumulateThenWait returns a workflow that accumulates n AddOne calls (building a
// non-empty committed history, so the sidecar becomes warm for it and the worker
// caches it), then parks on an external event named "go". While parked the
// instance is idle, so the worker's cached history can be reclaimed (by TTL or the
// size cap) even though the sidecar stays warm. Raising "go" then dispatches a
// delta the worker must recover via GetInstanceHistory.
func accumulateThenWait(n int) func(*task.WorkflowContext) (any, error) {
	return func(ctx *task.WorkflowContext) (any, error) {
		total := 0
		for i := 0; i < n; i++ {
			var got int
			if err := ctx.CallActivity("AddOne", task.WithActivityInput(total)).Await(&got); err != nil {
				return nil, err
			}
			total = got
		}
		if err := ctx.WaitForSingleEvent("go", -1).Await(nil); err != nil {
			return nil, err
		}
		return total, nil
	}
}
