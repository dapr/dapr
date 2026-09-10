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
	failErr          error
	failNotifyCh     chan struct{}
	failedCount      atomic.Int32

	multiObserver func(*state.TransactionalStateRequest)

	multiDeleteHold *holdSpec
	bulkGetHold     *holdSpec
	getHold         *holdSpec

	getFailKeySubstring string
	getFailRemaining    int
	getFailNotifyCh     chan struct{}

	getEmptyKeySubstring string
	getEmptyRemaining    int
	getEmptyNotifyCh     chan struct{}
}

// holdSpec is a one-shot arm-able hold on a store operation matching a key
// substring. arrived is closed when the operation is captured; the operation
// then blocks until releaseCh is closed (or the request context is done);
// done, when non-nil, is closed after the delegated operation returns.
type holdSpec struct {
	sub       string
	arrived   chan struct{}
	releaseCh chan struct{}
	done      chan struct{}
}

// SetMultiObserver registers a callback invoked synchronously on every Multi
// before delegation. Tests use it to inspect the ETag of specific upserts.
// nil clears any previously registered observer.
func (s *Store) SetMultiObserver(fn func(*state.TransactionalStateRequest)) {
	s.mu.Lock()
	s.multiObserver = fn
	s.mu.Unlock()
}

// New returns a Store that is functionally identical to the in-memory store
// until ArmFailures is called.
func New(t *testing.T) *Store {
	return &Store{
		Wrapped: inmemory.New(t).(*inmemory.Wrapped),
	}
}

// ArmFailures arms the store to return a synthetic transient error on the next
// n transactional Multi requests whose operation keys contain keySubstring.
// n=1 produces a one-shot failure, n=0 disarms. If notify is non-nil it is
// closed the FIRST time a matching Multi is failed, so callers can synchronise
// on the failure firing.
func (s *Store) ArmFailures(keySubstring string, n int, notify chan struct{}) {
	s.armWith(keySubstring, n, errors.New("fault.Store: injected transient failure"), notify)
}

// ArmETagMismatch arms the store to return a state.ETagError of kind
// ETagMismatch on the next n matching Multi requests. Use this when a test
// wants to exercise the orchestrator's peer-write recovery path rather than
// a generic transient error.
func (s *Store) ArmETagMismatch(keySubstring string, n int, notify chan struct{}) {
	s.armWith(keySubstring, n,
		state.NewETagError(state.ETagMismatch, errors.New("fault.Store: injected etag mismatch")),
		notify)
}

func (s *Store) armWith(keySubstring string, n int, err error, notify chan struct{}) {
	s.mu.Lock()
	s.failKeySubstring = keySubstring
	s.failRemaining = n
	s.failErr = err
	s.failNotifyCh = notify
	s.mu.Unlock()
}

// FailedCount returns the total number of Multi requests that have been failed
// by this Store since it was constructed.
func (s *Store) FailedCount() int { return int(s.failedCount.Load()) }

// ArmMultiDeleteHold arms a one-shot hold on the next Multi containing a
// Delete operation whose key contains sub. arrived is closed when the Multi
// is captured; the Multi blocks until release is called (or its context is
// done); done is closed after the delegated Multi returns. release is
// idempotent, so it is safe to register with t.Cleanup.
func (s *Store) ArmMultiDeleteHold(sub string) (arrived <-chan struct{}, release func(), done <-chan struct{}) {
	spec := &holdSpec{
		sub:       sub,
		arrived:   make(chan struct{}),
		releaseCh: make(chan struct{}),
		done:      make(chan struct{}),
	}
	s.mu.Lock()
	s.multiDeleteHold = spec
	s.mu.Unlock()

	var once sync.Once
	return spec.arrived, func() { once.Do(func() { close(spec.releaseCh) }) }, spec.done
}

// ArmBulkGetHold arms a one-shot hold on the next BulkGet touching a key
// containing sub. arrived is closed when the BulkGet is captured; the call
// blocks until release is called (or its context is done). release is
// idempotent, so it is safe to register with t.Cleanup.
func (s *Store) ArmBulkGetHold(sub string) (arrived <-chan struct{}, release func()) {
	spec := &holdSpec{
		sub:       sub,
		arrived:   make(chan struct{}),
		releaseCh: make(chan struct{}),
	}
	s.mu.Lock()
	s.bulkGetHold = spec
	s.mu.Unlock()

	var once sync.Once
	return spec.arrived, func() { once.Do(func() { close(spec.releaseCh) }) }
}

// ArmGetHold arms a one-shot hold on the next Get whose key contains sub.
// The Get blocks until release is called (or its context is done).
func (s *Store) ArmGetHold(sub string) (arrived <-chan struct{}, release func()) {
	spec := &holdSpec{
		sub:       sub,
		arrived:   make(chan struct{}),
		releaseCh: make(chan struct{}),
	}
	s.mu.Lock()
	s.getHold = spec
	s.mu.Unlock()

	var once sync.Once
	return spec.arrived, func() { once.Do(func() { close(spec.releaseCh) }) }
}

// ArmGetFailures arms the proxy to fail the next n Gets whose key contains
// keySubstring with a transient error. n=0 disarms. notify, when non-nil, is
// closed on the first injected failure.
func (s *Store) ArmGetFailures(keySubstring string, n int, notify chan struct{}) {
	s.mu.Lock()
	s.getFailKeySubstring = keySubstring
	s.getFailRemaining = n
	s.getFailNotifyCh = notify
	s.mu.Unlock()
}

// ArmGetEmpty arms the store to answer the next n Gets whose key contains
// keySubstring with an empty response, as a store whose reads lag its own
// writes would. n=0 disarms. notify, when non-nil, is closed on the first.
func (s *Store) ArmGetEmpty(keySubstring string, n int, notify chan struct{}) {
	s.mu.Lock()
	s.getEmptyKeySubstring = keySubstring
	s.getEmptyRemaining = n
	s.getEmptyNotifyCh = notify
	s.mu.Unlock()
}

// Get implements state.Store, honouring armed Get failures, empty answers
// and holds.
func (s *Store) Get(ctx context.Context, req *state.GetRequest) (*state.GetResponse, error) {
	s.mu.Lock()
	if s.getEmptyKeySubstring != "" && s.getEmptyRemaining > 0 && strings.Contains(req.Key, s.getEmptyKeySubstring) {
		s.getEmptyRemaining--
		notify := s.getEmptyNotifyCh
		s.getEmptyNotifyCh = nil
		s.mu.Unlock()
		if notify != nil {
			close(notify)
		}
		return &state.GetResponse{}, nil
	}
	if s.getFailKeySubstring != "" && s.getFailRemaining > 0 && strings.Contains(req.Key, s.getFailKeySubstring) {
		s.getFailRemaining--
		notify := s.getFailNotifyCh
		s.getFailNotifyCh = nil
		s.mu.Unlock()
		if notify != nil {
			close(notify)
		}
		return nil, errors.New("fault.Store: injected transient get failure")
	}
	var hold *holdSpec
	if s.getHold != nil && strings.Contains(req.Key, s.getHold.sub) {
		hold = s.getHold
		s.getHold = nil
	}
	s.mu.Unlock()

	if hold != nil {
		close(hold.arrived)
		select {
		case <-hold.releaseCh:
		case <-ctx.Done():
		}
	}

	return s.Store.Get(ctx, req)
}

// Multi implements state.TransactionalStore. If the store is armed and the
// request touches a key containing the armed substring, the request is failed
// with the armed error; otherwise the request is delegated to the underlying
// in-memory store.
func (s *Store) Multi(ctx context.Context, req *state.TransactionalStateRequest) error {
	s.mu.Lock()
	obs := s.multiObserver
	s.mu.Unlock()

	if obs != nil {
		obs(req)
	}

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
	var (
		notify chan struct{}
		err    error
	)
	if shouldFail {
		s.failRemaining--
		s.failedCount.Add(1)
		err = s.failErr
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
		return err
	}

	s.mu.Lock()
	var hold *holdSpec
	if s.multiDeleteHold != nil && anyDeleteHasSubstring(req.Operations, s.multiDeleteHold.sub) {
		hold = s.multiDeleteHold
		s.multiDeleteHold = nil
	}
	s.mu.Unlock()

	if hold != nil {
		close(hold.arrived)
		select {
		case <-hold.releaseCh:
		case <-ctx.Done():
		}
		defer close(hold.done)
	}

	return s.Wrapped.Store.(state.TransactionalStore).Multi(ctx, req)
}

// BulkGet shadows the promoted in-memory implementation so an armed hold can
// block the read between a caller's metadata Get and its bulk entry read.
func (s *Store) BulkGet(ctx context.Context, req []state.GetRequest, opts state.BulkGetOpts) ([]state.BulkGetResponse, error) {
	s.mu.Lock()
	var hold *holdSpec
	if s.bulkGetHold != nil {
		for _, r := range req {
			if strings.Contains(r.Key, s.bulkGetHold.sub) {
				hold = s.bulkGetHold
				s.bulkGetHold = nil
				break
			}
		}
	}
	s.mu.Unlock()

	if hold != nil {
		close(hold.arrived)
		select {
		case <-hold.releaseCh:
		case <-ctx.Done():
		}
	}

	return s.Store.BulkGet(ctx, req, opts)
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

func anyDeleteHasSubstring(ops []state.TransactionalStateOperation, sub string) bool {
	for _, op := range ops {
		if del, ok := op.(state.DeleteRequest); ok && strings.Contains(del.Key, sub) {
			return true
		}
	}
	return false
}
