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

package placement

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/placement/hashing"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/kit/concurrency/fifo"
	"github.com/dapr/kit/concurrency/lock"
)

type stubTable struct {
	table.Interface
}

func (stubTable) Types() []string { return nil }

func newTestPlacement(t *testing.T, failsafe time.Duration) (*placement, context.Context) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	l := lock.NewOuterCancel(errors.New("placement is disseminating"), time.Second*2)
	go l.Run(ctx)

	return &placement{
		lock:          l,
		operationLock: fifo.New(),
		hashTable: &hashing.ConsistentHashTables{
			Entries: make(map[string]*hashing.Consistent),
		},
		actorTable:            stubTable{},
		htarget:               healthz.New().AddTarget("placement-test"),
		readyCh:               make(chan struct{}),
		unlockFailsafeTimeout: failsafe,
	}, ctx
}

func (p *placement) receive(ctx context.Context, operation string) {
	p.handleReceive(ctx, &v1pb.PlacementOrder{Operation: operation})
}

func requireTableUnlocked(t *testing.T, ctx context.Context, p *placement) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		rctx, rcancel := context.WithTimeout(ctx, time.Millisecond*50)
		defer rcancel()
		_, cancel, err := p.lock.RLock(rctx)
		if assert.NoError(c, err) {
			cancel()
		}
	}, time.Second*2, time.Millisecond*10)
}

func requireTableLocked(t *testing.T, ctx context.Context, p *placement) {
	t.Helper()
	rctx, rcancel := context.WithTimeout(ctx, time.Millisecond*100)
	defer rcancel()
	_, cancel, err := p.lock.RLock(rctx)
	if err == nil {
		cancel()
	}
	require.Error(t, err, "expected table to be locked")
}

// A lock order whose unlock arrives after the failsafe already released the
// round must not desync the lock/unlock counters: subsequent rounds must
// still unlock the table.
func Test_lateUnlockAfterFailsafe(t *testing.T) {
	p, ctx := newTestPlacement(t, time.Millisecond*200)

	p.receive(ctx, lockOperation)
	requireTableLocked(t, ctx, p)

	// Failsafe releases the round.
	requireTableUnlocked(t, ctx, p)

	// The late unlock for the released round arrives anyway.
	p.receive(ctx, unlockOperation)

	// The next round must lock and unlock normally.
	p.receive(ctx, lockOperation)
	requireTableLocked(t, ctx, p)
	p.receive(ctx, unlockOperation)
	requireTableUnlocked(t, ctx, p)

	// And the round after that.
	p.receive(ctx, lockOperation)
	requireTableLocked(t, ctx, p)
	p.receive(ctx, unlockOperation)
	requireTableUnlocked(t, ctx, p)
}

// Two lock orders coalesced into one held lock with only a single unlock
// must be released by the failsafe, and later rounds must pair correctly.
func Test_coalescedLocksSingleUnlock(t *testing.T) {
	p, ctx := newTestPlacement(t, time.Millisecond*200)

	p.receive(ctx, lockOperation)
	p.receive(ctx, lockOperation)
	requireTableLocked(t, ctx, p)

	p.receive(ctx, unlockOperation)

	// The failsafe must release the held lock even though the counters do
	// not pair.
	requireTableUnlocked(t, ctx, p)

	p.receive(ctx, lockOperation)
	requireTableLocked(t, ctx, p)
	p.receive(ctx, unlockOperation)
	requireTableUnlocked(t, ctx, p)
}

// An unlock received with no prior lock, as happens when joining a placement
// stream mid round, must not poison the counters for future rounds.
func Test_unlockWithoutLock(t *testing.T) {
	p, ctx := newTestPlacement(t, time.Millisecond*200)

	p.receive(ctx, unlockOperation)
	requireTableUnlocked(t, ctx, p)

	p.receive(ctx, lockOperation)
	requireTableLocked(t, ctx, p)
	p.receive(ctx, unlockOperation)
	requireTableUnlocked(t, ctx, p)
}

// A well behaved lock/unlock round must release the table immediately via
// the unlock order, not the failsafe.
func Test_pairedLockUnlock(t *testing.T) {
	p, ctx := newTestPlacement(t, time.Second*15)

	p.receive(ctx, lockOperation)
	requireTableLocked(t, ctx, p)
	p.receive(ctx, unlockOperation)

	rctx, rcancel := context.WithTimeout(ctx, time.Millisecond*100)
	defer rcancel()
	_, cancel, err := p.lock.RLock(rctx)
	require.NoError(t, err)
	cancel()
}
