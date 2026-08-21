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

package standdown

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/security/fake"
)

func TestInactiveByDefault(t *testing.T) {
	t.Parallel()
	s := New(Options{Security: fake.New()})
	assert.False(t, s.Active())
}

// TestNoAddressesNeverStandsDown asserts a placement service without
// scheduler addresses serves unconditionally: Run blocks until cancelled and
// never activates.
func TestNoAddressesNeverStandsDown(t *testing.T) {
	t.Parallel()

	var called bool
	s := New(Options{
		Security:    fake.New(),
		OnStandDown: func() { called = true },
	})

	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond*100)
	defer cancel()

	err := s.Run(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, s.Active())
	assert.False(t, called)
}

// TestUnreachableSchedulersKeepServing asserts unreachable schedulers fail
// open: the watcher retries rather than standing down, so a scheduler outage
// cannot take the placement service with it.
func TestUnreachableSchedulersKeepServing(t *testing.T) {
	t.Parallel()

	var called bool
	s := New(Options{
		Addresses:   []string{"127.0.0.1:1"},
		Security:    fake.New(),
		OnStandDown: func() { called = true },
	})

	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond*500)
	defer cancel()

	err := s.Run(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, s.Active())
	assert.False(t, called)
}

// TestHungSchedulerCompletesFirstObservation asserts a scheduler which
// accepts the connection but never answers does not block serving: the
// first observation completes on its timeout.
func TestHungSchedulerCompletesFirstObservation(t *testing.T) {
	t.Parallel()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { lis.Close() })
	go func() {
		for {
			conn, aerr := lis.Accept()
			if aerr != nil {
				return
			}
			// Hold the connection open without ever answering.
			t.Cleanup(func() { conn.Close() })
		}
	}()

	s := New(Options{
		Addresses: []string{lis.Addr().String()},
		Security:  fake.New(),
	})

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go s.Run(ctx)

	select {
	case <-s.FirstObservation():
	case <-time.After(firstObservationTimeout + time.Second*5):
		require.Fail(t, "a hung scheduler watch must not block the first observation")
	}
	assert.False(t, s.Active())
}
