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

// Package fault provides a state.Store implementation that wraps the
// in-memory store and lets tests deterministically inject transient
// transactional save failures keyed by a substring of the operation key.
// Tests use this to verify that workflow / actor code recovers cleanly
// from state-store hiccups (no stuck workflows, no orphan reminders).
package fault

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dapr/components-contrib/state"

	"github.com/dapr/dapr/tests/integration/framework/process/statestore/inmemory"
)

// Store wraps an in-memory state store and selectively fails Multi
// (transactional) operations whose keys contain a configured substring.
type Store struct {
	*inmemory.Wrapped

	mu sync.Mutex

	failKeySubstring string
	failRemaining    int
	failNotifyCh     chan struct{}
	failedCount      atomic.Int32
}

// New returns a Store that is functionally identical to the in-memory store
// until ArmFailures is called.
func New(t *testing.T) *Store {
	return &Store{
		Wrapped: inmemory.New(t).(*inmemory.Wrapped),
	}
}

// ArmFailures arms the store to return an injected transient error on the next
// n transactional Multi requests whose operation keys contain keySubstring.
// n=1 produces a one-shot failure, n=0 disarms. If notify is non-nil it is
// closed the FIRST time a matching Multi is failed, so callers can synchronise
// on the failure firing.
func (s *Store) ArmFailures(keySubstring string, n int, notify chan struct{}) {
	s.mu.Lock()
	s.failKeySubstring = keySubstring
	s.failRemaining = n
	s.failNotifyCh = notify
	s.mu.Unlock()
}

// FailedCount returns the total number of Multi requests that have been failed
// by this Store since it was constructed.
func (s *Store) FailedCount() int { return int(s.failedCount.Load()) }

// Multi implements state.TransactionalStore. If the store is armed and the
// request touches a key containing the armed substring, the request is failed
// with a synthetic transient error; otherwise the request is delegated to the
// underlying in-memory store.
func (s *Store) Multi(ctx context.Context, req *state.TransactionalStateRequest) error {
	s.mu.Lock()

	keys := make([]string, 0, len(req.Operations))
	for _, op := range req.Operations {
		switch v := op.(type) {
		case state.SetRequest:
			keys = append(keys, v.Key)
		case state.DeleteRequest:
			keys = append(keys, v.Key)
		}
	}

	shouldFail := s.failKeySubstring != "" && s.failRemaining > 0 && anyHasSubstring(keys, s.failKeySubstring)
	var notify chan struct{}
	if shouldFail {
		s.failRemaining--
		s.failedCount.Add(1)
		if s.failNotifyCh != nil {
			notify = s.failNotifyCh
			s.failNotifyCh = nil
		}
	}
	s.mu.Unlock()

	if shouldFail {
		if notify != nil {
			close(notify)
		}
		return errors.New("fault.Store: injected transient failure")
	}
	return s.Wrapped.Store.(state.TransactionalStore).Multi(ctx, req)
}

// MultiMaxSize advertises no per-transaction key limit so the test can freely
// batch saves through the in-memory implementation.
func (s *Store) MultiMaxSize() int { return -1 }

func anyHasSubstring(keys []string, sub string) bool {
	for _, k := range keys {
		if strings.Contains(k, sub) {
			return true
		}
	}
	return false
}
