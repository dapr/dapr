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

// Package cosmosbatch provides a state.Store implementation that wraps the
// in-memory store and mirrors Azure Cosmos DB's transactional-batch
// semantics: if any delete operation in a Multi targets a key that does not
// exist, the whole batch is rejected with a generic "transaction failed"
// error and none of the operations are applied. The real Cosmos DB component
// (components-contrib/state/azure/cosmosdb) cannot tolerate a 404 on an
// individual delete inside a batch even though the component is structured
// to permit etag-less delete-on-missing outside transactions; tests use this
// wrapper to reproduce that asymmetry without booting Cosmos.
package cosmosbatch

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/dapr/components-contrib/state"

	"github.com/dapr/dapr/tests/integration/framework/process/statestore/inmemory"
)

// Store wraps an in-memory state store and rejects any Multi whose delete
// operations reference a key not currently present in the store.
type Store struct {
	*inmemory.Wrapped

	rejectedCount atomic.Int32
}

func New(t *testing.T) *Store {
	return &Store{
		Wrapped: inmemory.New(t).(*inmemory.Wrapped),
	}
}

// RejectedCount returns the number of Multi requests this Store has refused
// because they contained a delete for a missing key.
func (s *Store) RejectedCount() int { return int(s.rejectedCount.Load()) }

// Multi implements state.TransactionalStore. Before delegating, every delete
// in req is checked against the underlying store; if any references a key that
// does not currently exist, the whole batch is rejected with the "transaction
// failed" error that the real Cosmos component returns when an operation fails
// with FailedDependency status code.
func (s *Store) Multi(ctx context.Context, req *state.TransactionalStateRequest) error {
	for _, op := range req.Operations {
		del, ok := op.(state.DeleteRequest)
		if !ok {
			continue
		}
		res, err := s.Get(ctx, &state.GetRequest{Key: del.Key})
		if err != nil {
			return err
		}
		if res == nil || (len(res.Data) == 0 && res.ETag == nil) {
			s.rejectedCount.Add(1)
			return errors.New("transaction failed")
		}
	}

	return s.Store.(state.TransactionalStore).Multi(ctx, req)
}

// MultiMaxSize advertises no per-transaction key limit; the cap is enforced by
// the real Cosmos component and is not what this wrapper is exercising.
func (s *Store) MultiMaxSize() int { return -1 }
