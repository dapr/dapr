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
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
)

// backdateRefusal moves taskID's refusal out of the window the sender spends,
// which is the only thing that releases a refused dispatch.
func backdateRefusal(a *awaitedResults, taskID int32) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e := a.m[taskID]
	e.refusedAt = time.Now().Add(-common.PublishRetryWindow() - time.Second)
	a.m[taskID] = e
}

func Test_awaitedResults_settleMatchesTheExecution(t *testing.T) {
	t.Parallel()

	var a awaitedResults
	a.arm(7, "exec-B")
	a.settle(7, "exec-A")
	assert.True(t, a.any(), "a result of another execution settles nothing")
	a.settle(7, "exec-B")
	assert.False(t, a.any())

	// Empty on either side matches: synthetic failures and pre-execution-id
	// SDKs carry none.
	a.arm(1, "")
	a.settle(1, "anything")
	assert.False(t, a.any())
	a.arm(2, "exec-C")
	a.settle(2, "")
	assert.False(t, a.any())
}

func Test_awaitedResults_oneResultSettlesOnlyItsOwnDispatch(t *testing.T) {
	t.Parallel()

	var a awaitedResults
	a.arm(0, "exec-A")
	a.arm(1, "exec-B")
	a.settle(0, "exec-A")
	assert.True(t, a.any(), "the second dispatch is still owed")
	a.settle(1, "exec-B")
	assert.False(t, a.any())
}

// A refusal is not a verdict the guard can act on: the sender keeps the
// result in hand and a later retry routinely admits it. The ID is held until
// no redelivery can exist.
func Test_awaitedResults_refusalHoldsForTheSendersWindow(t *testing.T) {
	t.Parallel()

	var a awaitedResults
	a.arm(0, "exec-A")
	a.refuse(0, "exec-A")
	assert.True(t, a.any(), "the sender is still retrying the result in hand")

	backdateRefusal(&a, 0)
	assert.False(t, a.any(), "nothing can re-deliver it once the window has passed")
}

func Test_awaitedResults_refusalOfAnotherExecutionIsIgnored(t *testing.T) {
	t.Parallel()

	var a awaitedResults
	a.arm(0, "exec-B")
	a.refuse(0, "exec-A")

	a.mu.Lock()
	refusedAt := a.m[0].refusedAt
	a.mu.Unlock()
	assert.True(t, refusedAt.IsZero(), "a straggler's refusal must not start the clock on the live dispatch")
	assert.True(t, a.any())
}

func Test_awaitedResults_redispatchOwesTheResultAfresh(t *testing.T) {
	t.Parallel()

	var a awaitedResults
	a.arm(0, "exec-A")
	a.refuse(0, "exec-A")
	backdateRefusal(&a, 0)
	assert.False(t, a.any())

	a.arm(0, "exec-B")
	assert.True(t, a.any(), "a new generation's dispatch is owed whatever happened to the last")
}

// An admitted or acked result settles outright: the window only bounds a
// refusal, which has no other end.
func Test_awaitedResults_settleAfterRefusalIsImmediate(t *testing.T) {
	t.Parallel()

	var a awaitedResults
	a.arm(0, "exec-A")
	a.refuse(0, "exec-A")
	a.settle(0, "exec-A")
	assert.False(t, a.any())
}
