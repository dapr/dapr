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

package watchhosts

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/healthz"
	healthzfake "github.com/dapr/dapr/pkg/healthz/fake"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/clients"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/loops"
	"github.com/dapr/dapr/pkg/runtime/scheduler/leadership"
	"github.com/dapr/dapr/pkg/security/fake"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

// TestUnreachableReportBackoff asserts the report comes after the delay, and
// then with a gap that doubles up to the cap, not on every failed attempt.
func TestUnreachableReportBackoff(t *testing.T) {
	require.Equal(t, 5*time.Second, unreachableReportDelay(),
		"the test expects the default delay")

	u := newUnreachable([]string{"127.0.0.1:50006"}, healthzfake.New())

	start := time.Unix(0, 0)
	var reports []time.Duration
	// One failed attempt every second for 20 minutes.
	for s := range 20 * 60 {
		elapsed, ok := u.due(start.Add(time.Duration(s) * time.Second))
		if ok {
			reports = append(reports, elapsed)
		}
	}

	assert.Equal(t, []time.Duration{
		5 * time.Second,
		15 * time.Second,
		35 * time.Second,
		75 * time.Second,
		155 * time.Second,
		315 * time.Second,
		615 * time.Second,
		915 * time.Second,
	}, reports)
}

// TestRunSetsNotReadyReason asserts a Scheduler address that refuses
// connections keeps the target not ready, with the address as the reason. The
// reason does not include the error text.
func TestRunSetsNotReadyReason(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	h := healthz.New()
	w := New(Options{
		Addresses:  []string{addr},
		Healthz:    h,
		Security:   fake.New(),
		Clients:    clients.New(clients.Options{Security: fake.New()}),
		HostLoop:   loopfake.New[loops.EventHost](),
		Leadership: leadership.New(),
	})

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() { errCh <- w.Run(ctx) }()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		targets := h.GetUnhealthyTargets()
		if assert.Len(c, targets, 1) {
			assert.Equal(c, "scheduler-watch-hosts: cannot connect to the Scheduler at "+addr+", see the daprd log", targets[0])
		}
	}, 10*time.Second, 10*time.Millisecond)
	assert.False(t, h.IsReady())

	cancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		require.Fail(t, "Run did not return after cancel")
	}
}

// TestRunNoReportOnShutdown asserts that an attempt that fails because the
// context is canceled gives no report line and no reason, also when a report
// is due.
func TestRunNoReportOnShutdown(t *testing.T) {
	buf := new(bytes.Buffer)
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(os.Stdout) })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	h := healthz.New()
	w := New(Options{
		Addresses:  []string{addr},
		Healthz:    h,
		Security:   fake.New(),
		Clients:    clients.New(clients.Options{Security: fake.New()}),
		HostLoop:   loopfake.New[loops.EventHost](),
		Leadership: leadership.New(),
	})

	// The first failed attempt was one hour ago, so a report is due on the
	// next failed attempt.
	_, ok := w.unreachable.due(time.Now().Add(-time.Hour))
	require.False(t, ok)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, w.Run(ctx), context.Canceled)

	assert.Contains(t, buf.String(), "Failed to connect to scheduler host")
	assert.NotContains(t, buf.String(), "Cannot connect to the Scheduler")
	assert.Equal(t, []string{"scheduler-watch-hosts"}, h.GetUnhealthyTargets())

	// Control: a failed attempt while daprd runs gives the report.
	w.unreachable.failed(errors.New("connection refused"))
	assert.Contains(t, buf.String(), "Cannot connect to the Scheduler at "+addr+" for 1h")
	assert.Contains(t, buf.String(), ": connection refused. daprd is not ready until it connects.")
}
