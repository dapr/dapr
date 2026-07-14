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

package state

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/payloadstore"
)

// maxConcurrentPuts bounds the store writes issued in parallel for one
// offload pass. Turns rarely add more than a handful of offloadable
// events; the bound only guards pathological fan-out turns.
const maxConcurrentPuts = 8

// OffloadNewPayloads replaces the user payload of every newly added inbox
// and history event the store elects to offload (Store.ShouldOffload)
// with an encoded payload-store reference (Put first, then swap in
// place). It must run before the new events are signed or marshaled for
// persistence so the stored (and signed) bytes carry references.
//
// Only events added since the last save are walked: persisted events are
// immutable and, when signing is enabled, already covered by signatures.
// The pass is idempotent - a value that already decodes as a reference is
// skipped - so retried turns and replay never double-encode. On error the
// state must not be persisted: the caller is expected to discard the
// cached state and retry the turn, which rebuilds the events and repeats
// the (idempotent, content-addressed) Puts.
//
// A nil store or a state with no new events is a no-op.
func (s *State) OffloadNewPayloads(ctx context.Context, store payloadstore.Store, instanceID string) error {
	if store == nil {
		return nil
	}

	newInbox := s.Inbox[len(s.Inbox)-s.inboxAddedCount:]
	if err := offloadEvents(ctx, store, instanceID, newInbox); err != nil {
		return fmt.Errorf("failed to offload new inbox event payloads: %w", err)
	}

	newHistory := s.History[len(s.History)-s.historyAddedCount:]
	if err := offloadEvents(ctx, store, instanceID, newHistory); err != nil {
		return fmt.Errorf("failed to offload new history event payloads: %w", err)
	}

	return nil
}

func offloadEvents(ctx context.Context, store payloadstore.Store, instanceID string, events []*backend.HistoryEvent) error {
	// Select the events to offload sequentially - the checks are cheap -
	// then fan out the store writes, which may be remote I/O. Each
	// goroutine owns exactly one event's payload field, and Store
	// implementations are required to be safe for concurrent use.
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(maxConcurrentPuts)

	for _, e := range events {
		p := payloadstore.Payload(e)
		if p == nil || !store.ShouldOffload(len(p.GetValue())) {
			continue
		}

		// Skip values that already decode as a reference. This makes the
		// pass idempotent across retried turns. A value that merely
		// carries the magic prefix but fails a strict decode is user
		// data and is offloaded like any other payload, preserving its
		// bytes exactly.
		if _, err := payloadstore.DecodeReference(p.GetValue()); err == nil {
			continue
		}

		eventID := e.GetEventId()
		eg.Go(func() error {
			ref, err := store.Put(egCtx, instanceID, []byte(p.GetValue()))
			if err != nil {
				return fmt.Errorf("failed to store payload of event %d: %w", eventID, err)
			}

			p.Value = payloadstore.EncodeReference(ref)
			return nil
		})
	}

	return eg.Wait()
}
