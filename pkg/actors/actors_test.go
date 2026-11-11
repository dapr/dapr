/*
Copyright 2024 The Dapr Authors
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

package actors

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	daprt "github.com/dapr/dapr/pkg/testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contribstate "github.com/dapr/components-contrib/state"
	inmemory "github.com/dapr/components-contrib/state/in-memory"
	"github.com/dapr/dapr/pkg/actors/api"
	actorstate "github.com/dapr/dapr/pkg/actors/state"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/kit/logger"
)

func TestHostValidation(t *testing.T) {
	t.Parallel()

	t.Run("kubernetes mode with mTLS, missing namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(true, modes.KubernetesMode, "")
		require.Error(t, err)
	})

	t.Run("kubernetes mode without mTLS, missing namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(false, modes.KubernetesMode, "")
		require.NoError(t, err)
	})

	t.Run("kubernetes mode with mTLS and namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(true, modes.KubernetesMode, "default")
		require.NoError(t, err)
	})

	t.Run("self hosted mode with mTLS, missing namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(true, modes.StandaloneMode, "")
		require.NoError(t, err)
	})

	t.Run("self hosted mode without mTLS, missing namespace", func(t *testing.T) {
		err := ValidateHostEnvironment(false, modes.StandaloneMode, "")
		require.NoError(t, err)
	})
}

func TestConvergeHosting(t *testing.T) {
	t.Parallel()

	// newConverge returns an actors runtime whose table records the ordered
	// sequence of suspend/resume operations.
	newConverge := func(t *testing.T) (*actors, *compstore.ComponentStore, *[]string) {
		t.Helper()

		var ops []string
		cs := compstore.New()
		a := &actors{
			compStore: cs,
			table: tablefake.New().
				WithSuspendHosting(func(context.Context) error {
					ops = append(ops, "suspend")
					return nil
				}).
				WithResumeHosting(func() {
					ops = append(ops, "resume")
				}),
		}
		_, a.hostingName, a.hostingRev, a.hostingActive = cs.GetStateStoreActorWithRevision()
		return a, cs, &ops
	}

	store := func(t *testing.T) contribstate.Store {
		t.Helper()
		return inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name()))
	}

	t.Run("no change is a no-op", func(t *testing.T) {
		t.Parallel()
		a, _, ops := newConverge(t)

		a.convergeHosting(t.Context())
		assert.Empty(t, *ops)
	})

	t.Run("store added resumes without draining", func(t *testing.T) {
		t.Parallel()
		a, cs, ops := newConverge(t)

		require.NoError(t, cs.AddStateStoreActor("mystore", store(t)))
		a.convergeHosting(t.Context())
		assert.Equal(t, []string{"resume"}, *ops)

		// Converging again with no further change is a no-op.
		a.convergeHosting(t.Context())
		assert.Equal(t, []string{"resume"}, *ops)
	})

	t.Run("store removed drains and stays suspended", func(t *testing.T) {
		t.Parallel()
		a, cs, ops := newConverge(t)

		require.NoError(t, cs.AddStateStoreActor("mystore", store(t)))
		a.convergeHosting(t.Context())

		cs.DeleteStateStore("mystore")
		a.convergeHosting(t.Context())
		assert.Equal(t, []string{"resume", "suspend"}, *ops)
	})

	t.Run("same-name update swaps in place without draining", func(t *testing.T) {
		t.Parallel()
		a, cs, ops := newConverge(t)

		require.NoError(t, cs.AddStateStoreActor("mystore", store(t)))
		a.convergeHosting(t.Context())

		// A same-name update is a delete immediately followed by an add; the
		// data path resolves the store per call so hosted actors continue
		// against the new instance without a drain cycle.
		cs.DeleteStateStore("mystore")
		require.NoError(t, cs.AddStateStoreActor("mystore", store(t)))
		a.convergeHosting(t.Context())
		assert.Equal(t, []string{"resume"}, *ops)
	})

	t.Run("renamed store drains before resuming", func(t *testing.T) {
		t.Parallel()
		a, cs, ops := newConverge(t)

		require.NoError(t, cs.AddStateStoreActor("mystore", store(t)))
		a.convergeHosting(t.Context())

		// A different component becoming the actor state store is a
		// different backing store, so hosted actors are drained before
		// hosting resumes against the new store.
		cs.DeleteStateStore("mystore")
		require.NoError(t, cs.AddStateStoreActor("otherstore", store(t)))
		a.convergeHosting(t.Context())
		assert.Equal(t, []string{"resume", "suspend", "resume"}, *ops)
	})
}

// TestConvergeBoundStores verifies that removing or reloading a bound state
// store deactivates the actors of the actor types bound to it, and only
// those: their in-memory state, and any state an SDK caches, may no longer
// match the store.
func TestConvergeBoundStores(t *testing.T) {
	t.Parallel()

	store := func(t *testing.T) contribstate.Store {
		t.Helper()
		return inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name()))
	}

	// newConverge returns an actors runtime whose table records, for each
	// halt, the actor types it deactivates among the given ones.
	newConverge := func(t *testing.T) (*actors, *compstore.ComponentStore, *[][]string) {
		t.Helper()

		cs := compstore.New()
		require.NoError(t, cs.AddStateStoreActor("default", store(t)))
		cs.AddStateStore("wallet-transactions", store(t))
		cs.AddStateStore("wallet-balance", store(t))

		var halts [][]string
		a := &actors{
			compStore: cs,
			stateResolver: actorstate.NewResolver(actorstate.ResolverOptions{
				Stores: cs,
				Bindings: map[string]string{
					"TransactionActor": "wallet-transactions",
					"AuditActor":       "wallet-transactions",
					"BalanceActor":     "wallet-balance",
				},
			}),
			table: tablefake.New().WithHaltNonHosted(func(_ context.Context, keep func(*api.LookupActorRequest) bool) error {
				var halted []string
				for _, actorType := range []string{"AuditActor", "BalanceActor", "PlainActor", "TransactionActor"} {
					if !keep(&api.LookupActorRequest{ActorType: actorType, ActorID: "1"}) {
						halted = append(halted, actorType)
					}
				}
				halts = append(halts, halted)
				return nil
			}),
		}
		a.boundStoreRevs = a.currentBoundStoreRevs()
		return a, cs, &halts
	}

	t.Run("no change is a no-op", func(t *testing.T) {
		t.Parallel()
		a, cs, halts := newConverge(t)

		// Changes to unbound stores, the default one included, are ignored.
		cs.AddStateStore("plain", store(t))
		cs.DeleteStateStore("default")
		a.convergeBoundStores(t.Context())
		assert.Empty(t, *halts)
	})

	t.Run("removing a bound store deactivates its actor types", func(t *testing.T) {
		t.Parallel()
		a, cs, halts := newConverge(t)

		cs.DeleteStateStore("wallet-transactions")
		a.convergeBoundStores(t.Context())
		assert.Equal(t, [][]string{{"AuditActor", "TransactionActor"}}, *halts)

		// Converging again with no further change is a no-op.
		a.convergeBoundStores(t.Context())
		assert.Len(t, *halts, 1)
	})

	t.Run("reloading a bound store deactivates its actor types", func(t *testing.T) {
		t.Parallel()
		a, cs, halts := newConverge(t)

		// A hot reload closes the store and adds it again, possibly against
		// different data.
		cs.DeleteStateStore("wallet-balance")
		cs.AddStateStore("wallet-balance", store(t))
		a.convergeBoundStores(t.Context())
		assert.Equal(t, [][]string{{"BalanceActor"}}, *halts)
	})

	t.Run("adding a missing bound store back deactivates its actor types", func(t *testing.T) {
		t.Parallel()
		a, cs, halts := newConverge(t)

		cs.DeleteStateStore("wallet-balance")
		a.convergeBoundStores(t.Context())
		// Actors reactivated in between, for example by a reminder, ran
		// without their store.
		cs.AddStateStore("wallet-balance", store(t))
		a.convergeBoundStores(t.Context())
		assert.Equal(t, [][]string{{"BalanceActor"}, {"BalanceActor"}}, *halts)
	})

	t.Run("several bound stores changing are halted together", func(t *testing.T) {
		t.Parallel()
		a, cs, halts := newConverge(t)

		cs.DeleteStateStore("wallet-balance")
		cs.DeleteStateStore("wallet-transactions")
		a.convergeBoundStores(t.Context())
		assert.Equal(t, [][]string{{"AuditActor", "BalanceActor", "TransactionActor"}}, *halts)
	})
}

func TestOnActorStateStoreChanged(t *testing.T) {
	t.Parallel()

	a := &actors{storeKickCh: make(chan struct{}, 1)}
	// Never blocks, regardless of how many notifications are outstanding.
	a.OnActorStateStoreChanged()
	a.OnActorStateStoreChanged()
	a.OnActorStateStoreChanged()

	select {
	case <-a.storeKickCh:
	default:
		t.Fatal("expected a latched kick")
	}
	select {
	case <-a.storeKickCh:
		t.Fatal("expected kicks to coalesce")
	default:
	}
}

func TestWaitForReady(t *testing.T) {
	t.Parallel()

	newActors := func() *actors {
		return &actors{
			disabled: new(atomic.Pointer[error]),
			readyCh:  make(chan struct{}),
			closedCh: make(chan struct{}),
		}
	}
	doneCtx := func() context.Context {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx
	}

	t.Run("a ready runtime wins over a done ctx", func(t *testing.T) {
		t.Parallel()
		a := newActors()
		close(a.readyCh)
		for range 1000 {
			require.NoError(t, a.waitForReady(doneCtx()))
		}
	})

	t.Run("a closed runtime wins over a ready runtime and a done ctx", func(t *testing.T) {
		t.Parallel()
		a := newActors()
		close(a.readyCh)
		close(a.closedCh)
		for range 1000 {
			require.ErrorIs(t, a.waitForReady(doneCtx()), messages.ErrActorRuntimeClosed)
		}
	})

	t.Run("a runtime that is not ready returns on a done ctx", func(t *testing.T) {
		t.Parallel()
		a := newActors()
		require.ErrorIs(t, a.waitForReady(doneCtx()), messages.ErrActorRuntimeNotFound)
	})

	t.Run("a disabled ready runtime returns its error", func(t *testing.T) {
		t.Parallel()
		a := newActors()
		errDisabled := errors.New("disabled")
		a.disabled.Store(&errDisabled)
		close(a.readyCh)
		require.ErrorIs(t, a.waitForReady(context.Background()), errDisabled)
	})
}

func TestNew_ActorStateStoreBindings(t *testing.T) {
	newStore := func() contribstate.Store {
		return inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name()))
	}

	t.Run("bound actor type resolves to its state store", func(t *testing.T) {
		cs := compstore.New()
		cs.AddStateStore("wallet-transactions", newStore())
		a := New(Options{
			CompStore:        cs,
			ActorStateStores: map[string]string{"TransactionActor": "wallet-transactions"},
		}).(*actors)

		name, _, err := a.stateResolver.Resolve("TransactionActor")
		require.NoError(t, err)
		assert.Equal(t, "wallet-transactions", name)
	})

	t.Run("no bindings uses the default actor state store slot", func(t *testing.T) {
		a := New(Options{CompStore: compstore.New()}).(*actors)

		_, _, err := a.stateResolver.Resolve("TransactionActor")
		require.ErrorIs(t, err, messages.ErrActorRuntimeNotFound)
	})
}

func TestWarnUnusableStateStores(t *testing.T) {
	newStore := func() contribstate.Store {
		return inmemory.NewInMemoryStateStore(logger.NewLogger(t.Name()))
	}

	t.Run("warns for each binding whose store cannot be used", func(t *testing.T) {
		var buf bytes.Buffer
		l := logger.NewLogger(t.Name())
		l.SetOutput(&buf)

		cs := compstore.New()
		cs.AddStateStore("wallet-balance", newStore())
		cs.AddStateStore("wallet-audit", &daprt.MockStateStore{})
		r := actorstate.NewResolver(actorstate.ResolverOptions{
			Stores: cs,
			Bindings: map[string]string{
				"TransactionActor": "wallet-transactions",
				"AuditActor":       "wallet-audit",
				"BalanceActor":     "wallet-balance",
			},
		})

		warnUnusableStateStores(l, r)

		out := buf.String()
		assert.Contains(t, out, "level=warning")
		assert.Contains(t, out, "State operations of actor type TransactionActor will fail: actor type TransactionActor is bound to state store wallet-transactions, which is not loaded")
		assert.Contains(t, out, `State operations of actor type AuditActor will fail: actors: state store \"wallet-audit\" bound to actor type \"AuditActor\" must support both the ETag and transactional features`)
		assert.NotContains(t, out, "BalanceActor")
		// The gRPC code of the API error is not part of the log line.
		assert.NotContains(t, out, "code =")
		// Warnings are sorted by actor type, so the output is deterministic.
		assert.Less(t, strings.Index(out, "AuditActor"), strings.Index(out, "TransactionActor"))
	})

	t.Run("no warning when every binding resolves", func(t *testing.T) {
		var buf bytes.Buffer
		l := logger.NewLogger(t.Name())
		l.SetOutput(&buf)

		cs := compstore.New()
		cs.AddStateStore("wallet-transactions", newStore())
		r := actorstate.NewResolver(actorstate.ResolverOptions{
			Stores:   cs,
			Bindings: map[string]string{"TransactionActor": "wallet-transactions"},
		})

		warnUnusableStateStores(l, r)
		assert.Empty(t, buf.String())
	})
}

func TestWarnUnhostedBindings(t *testing.T) {
	bindings := map[string]string{
		"transactionactor": "wallet-transactions",
		"OrphanActor":      "wallet-orphans",
		"BalanceActor":     "wallet-balance",
	}

	var buf bytes.Buffer
	l := logger.NewLogger(t.Name())
	l.SetOutput(&buf)

	warnUnhostedBindings(l, bindings, []string{"TransactionActor", "BalanceActor"})

	out := buf.String()
	assert.Contains(t, out, "level=warning")
	assert.Contains(t, out, "Actor type transactionactor is bound to state store wallet-transactions, but this app does not host it; did you mean TransactionActor? Actor types are case-sensitive")
	assert.Contains(t, out, "Actor type OrphanActor is bound to state store wallet-orphans, but this app does not host it, so the binding has no effect")
	assert.NotContains(t, out, "BalanceActor is bound")
	assert.Less(t, strings.Index(out, "OrphanActor"), strings.Index(out, "transactionactor"))

	buf.Reset()
	warnUnhostedBindings(l, map[string]string{"BalanceActor": "wallet-balance"}, []string{"BalanceActor"})
	assert.Empty(t, buf.String())
}
