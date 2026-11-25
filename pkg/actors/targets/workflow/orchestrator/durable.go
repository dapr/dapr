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
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
)

// errTurnNotSaved settles the todo.Durable of a turn that returned without
// an error and without saving the worker's response.
var errTurnNotSaved = errors.New("the workflow turn ended without saving the response")

// testTurnSaveHold and testFailTurnSaves are test-only fault injections on
// the save at the end of a turn: the save waits testTurnSaveHold first, and
// the first testFailTurnSaves saves fail with a recoverable error. Not
// supported production knobs.
var (
	testTurnSaveHold = sync.OnceValue(func() time.Duration {
		return common.EnvDurationOr("DAPR_WORKFLOW_TEST_TURN_SAVE_HOLD", 0)
	})
	testFailTurnSaves = sync.OnceValue(func() int64 {
		return common.EnvInt64Or("DAPR_WORKFLOW_TEST_FAIL_TURN_SAVES", 0)
	})
	failedTurnSaves atomic.Int64
)

func holdOrFailTurnSaveForTest(ctx context.Context) error {
	if hold := testTurnSaveHold(); hold > 0 {
		log.Warnf("TEST INJECTION: holding the turn save for %s", hold)
		select {
		case <-time.After(hold):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if budget := testFailTurnSaves(); budget > 0 && failedTurnSaves.Add(1) <= budget {
		log.Warn("TEST INJECTION: failing the turn save")
		return wferrors.NewRecoverable(errors.New("test injection: turn save failed"))
	}
	return nil
}
