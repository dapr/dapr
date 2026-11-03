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
	"context"
	"fmt"
	"time"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
)

// describe names the clashing operation for a log line or an error.
func (d staleDuplicate) describe() string {
	what := fmt.Sprintf("%s operation %d", d.kind, d.id)
	switch {
	case d.committed != "" && d.recreated != "":
		return fmt.Sprintf("%s (committed '%s', turn response '%s')", what, d.committed, d.recreated)
	case d.committed != "":
		return fmt.Sprintf("%s (committed '%s')", what, d.committed)
	default:
		return what
	}
}

// rejectTurn records that runWorkflow rejected a turn whose response
// re-creates a committed operation, and returns the recoverable error that
// makes the driving reminder run the turn again. It counts the rejections in a
// row and sets the hold that InvokeReminder applies before it returns the
// error (see holdRejectedTurn): the interval between two scheduler retries is
// constant, so the growth has to come from the actor. Only the caller that
// holds the turn lock may call it.
func (o *orchestrator) rejectTurn(state *wfenginestate.State, dup staleDuplicate) error {
	o.rejectedTurns++
	o.rejectedTurnHold = common.RejectedTurnBackoff(o.rejectedTurns)

	log.Warnf("Workflow actor '%s': rejecting the turn at generation %d with %d committed history events, because its response re-creates committed %s; "+
		"rejected %d time(s) in a row, retrying after at least %s. "+
		"A turn that is rejected again and again means the workflow code is not deterministic, for example it schedules work in a Go defer, which also runs each time a turn blocks. "+
		"A stale completion delivery adopted across turns also causes this, and clears on the next try",
		o.actorID, state.Generation, len(state.History), dup.describe(), o.rejectedTurns, o.rejectedTurnHold)

	return wferrors.NewRecoverable(fmt.Errorf("turn response re-creates committed %s; the workflow code may not be deterministic, or a stale completion delivery was rejected",
		dup.describe()))
}

// takeRejectedTurnHold returns the hold set by the latest rejectTurn and
// clears it. The caller must hold the turn lock.
func (o *orchestrator) takeRejectedTurnHold() time.Duration {
	hold := o.rejectedTurnHold
	o.rejectedTurnHold = 0
	return hold
}

// holdRejectedTurn waits for hold, or until ctx ends. InvokeReminder calls it
// after it released the turn lock, so the wait does not block calls to the
// instance such as a terminate or a raised event.
func holdRejectedTurn(ctx context.Context, hold time.Duration) {
	if hold <= 0 {
		return
	}
	t := time.NewTimer(hold)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}
