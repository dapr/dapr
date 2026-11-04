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

package resiliency

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	resiliencyV1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	"github.com/dapr/kit/logger"
)

type logLine struct {
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

type logCapture struct {
	lock sync.Mutex
	buf  bytes.Buffer
}

func (c *logCapture) Write(p []byte) (int, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.buf.Write(p)
}

// lines returns the captured log lines at or above info level that are
// emitted by the policy runner, plus debug lines when withDebug is set.
func (c *logCapture) lines(t *testing.T, withDebug bool) []logLine {
	t.Helper()
	c.lock.Lock()
	defer c.lock.Unlock()
	var res []logLine
	for raw := range strings.SplitSeq(strings.TrimSpace(c.buf.String()), "\n") {
		if raw == "" {
			continue
		}
		var l logLine
		require.NoError(t, json.Unmarshal([]byte(raw), &l))
		if !strings.HasPrefix(l.Msg, "Resiliency retry policy ") &&
			!strings.HasPrefix(l.Msg, "Resiliency timeout policy ") &&
			!strings.HasPrefix(l.Msg, "Resiliency circuit breaker ") {
			continue
		}
		if l.Level == "debug" && !withDebug {
			continue
		}
		res = append(res, l)
	}
	return res
}

func newCapturingLogger(t *testing.T) (logger.Logger, *logCapture) {
	t.Helper()
	l := logger.NewLogger("dapr.resiliency.test." + t.Name())
	c := &logCapture{}
	l.SetOutput(c)
	l.EnableJSONOutput(true)
	l.SetOutputLevel(logger.DebugLevel)
	return l, c
}

func newLogTestResiliency(t *testing.T, l logger.Logger) *Resiliency {
	t.Helper()
	cfg := &resiliencyV1alpha.Resiliency{
		ObjectMeta: metav1.ObjectMeta{Name: "myresiliency"},
		Spec: resiliencyV1alpha.ResiliencySpec{
			Policies: resiliencyV1alpha.Policies{
				Timeouts: map[string]string{
					"downstream": "50ms",
				},
				Retries: map[string]resiliencyV1alpha.Retry{
					"threeTimes": {
						Policy:     "constant",
						Duration:   "5ms",
						MaxRetries: new(3),
					},
				},
				CircuitBreakers: map[string]resiliencyV1alpha.CircuitBreaker{
					"registryCB": {
						Trip:        "consecutiveFailures >= 2",
						Timeout:     "50ms",
						MaxRequests: 1,
					},
				},
			},
			Targets: resiliencyV1alpha.Targets{
				Apps: map[string]resiliencyV1alpha.EndpointPolicyNames{
					"party-registry": {
						Retry: "threeTimes",
					},
					"slow-app": {
						Timeout: "downstream",
					},
					"flaky-app": {
						CircuitBreaker: "registryCB",
					},
				},
				Actors: map[string]resiliencyV1alpha.ActorPolicyNames{
					"myactor": {
						Timeout:             "downstream",
						Retry:               "threeTimes",
						CircuitBreaker:      "registryCB",
						CircuitBreakerScope: "id",
					},
				},
				Components: map[string]resiliencyV1alpha.ComponentPolicyNames{
					"statestore": {
						Outbound: resiliencyV1alpha.PolicyNames{
							Timeout: "downstream",
							Retry:   "threeTimes",
						},
						Inbound: resiliencyV1alpha.PolicyNames{
							Retry: "threeTimes",
						},
					},
				},
			},
		},
	}
	return FromConfigurations(l, cfg)
}

func TestPolicyLogsRetry(t *testing.T) {
	t.Run("one line per retry and a recovery line", func(t *testing.T) {
		l, c := newCapturingLogger(t)
		r := newLogTestResiliency(t, l)

		var calls atomic.Int32
		policy := NewRunner[any](t.Context(), r.EndpointPolicy("party-registry", "party-registry:persons/1"))
		_, err := policy(func(ctx context.Context) (any, error) {
			if calls.Add(1) <= 2 {
				return nil, errors.New("boom")
			}
			return nil, nil
		})
		require.NoError(t, err)
		assert.Equal(t, int32(3), calls.Load())

		assert.Equal(t, []logLine{
			{Level: "warning", Msg: `Resiliency retry policy "threeTimes" triggered for app party-registry (persons/1): attempt 1 failed, retrying in 5ms`},
			{Level: "warning", Msg: `Resiliency retry policy "threeTimes" triggered for app party-registry (persons/1): attempt 2 failed, retrying in 5ms`},
			{Level: "info", Msg: `Resiliency retry policy "threeTimes" recovered app party-registry (persons/1) after 3 attempts`},
		}, c.lines(t, false))

		// The error detail is kept at debug level.
		debug := 0
		for _, line := range c.lines(t, true) {
			if line.Level == "debug" {
				debug++
				assert.Contains(t, line.Msg, "failed with error: boom")
			}
		}
		assert.Equal(t, 2, debug)
	})

	t.Run("gave up after retries are exhausted", func(t *testing.T) {
		l, c := newCapturingLogger(t)
		r := newLogTestResiliency(t, l)

		policy := NewRunner[any](t.Context(), r.EndpointPolicy("party-registry", "party-registry/persons/1"))
		_, err := policy(func(ctx context.Context) (any, error) {
			return nil, errors.New("boom")
		})
		require.Error(t, err)

		lines := c.lines(t, false)
		require.Len(t, lines, 4)
		for i, line := range lines[:3] {
			assert.Equal(t, "warning", line.Level)
			assert.Contains(t, line.Msg, `Resiliency retry policy "threeTimes" triggered for app party-registry (persons/1): attempt `+string(rune('1'+i))+` failed`)
		}
		assert.Equal(t, logLine{Level: "warning", Msg: `Resiliency retry policy "threeTimes" gave up on app party-registry (persons/1) after 4 attempts`}, lines[3])
	})

	t.Run("nothing is logged when the first attempt succeeds", func(t *testing.T) {
		l, c := newCapturingLogger(t)
		r := newLogTestResiliency(t, l)

		policy := NewRunner[any](t.Context(), r.EndpointPolicy("party-registry", "party-registry:persons/1"))
		_, err := policy(func(ctx context.Context) (any, error) {
			return nil, nil
		})
		require.NoError(t, err)
		assert.Empty(t, c.lines(t, true))
	})
}

func TestPolicyLogsTimeout(t *testing.T) {
	l, c := newCapturingLogger(t)
	r := newLogTestResiliency(t, l)

	policy := NewRunner[any](t.Context(), r.EndpointPolicy("slow-app", "slow-app:persons/1"))
	_, err := policy(func(ctx context.Context) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)

	assert.Equal(t, []logLine{
		{Level: "warning", Msg: `Resiliency timeout policy "downstream" (50ms) triggered for app slow-app (persons/1)`},
	}, c.lines(t, false))
}

func TestPolicyLogsCircuitBreaker(t *testing.T) {
	l, c := newCapturingLogger(t)
	r := newLogTestResiliency(t, l)

	run := func(fail bool) error {
		policy := NewRunner[any](t.Context(), r.EndpointPolicy("flaky-app", "flaky-app:persons/1"))
		_, err := policy(func(ctx context.Context) (any, error) {
			if fail {
				return nil, errors.New("boom")
			}
			return nil, nil
		})
		return err
	}

	require.Error(t, run(true))
	require.Error(t, run(true))
	assert.Equal(t, []logLine{
		{Level: "warning", Msg: `Resiliency circuit breaker "registryCB" opened for app flaky-app (persons/1) (was closed)`},
	}, c.lines(t, false))

	// Once the breaker's timeout elapses, the next call is let through.
	assert.EventuallyWithT(t, func(ct *assert.CollectT) {
		assert.NoError(ct, run(false))
	}, 2*time.Second, 10*time.Millisecond)

	assert.Equal(t, []logLine{
		{Level: "warning", Msg: `Resiliency circuit breaker "registryCB" opened for app flaky-app (persons/1) (was closed)`},
		{Level: "info", Msg: `Resiliency circuit breaker "registryCB" half-open for app flaky-app (persons/1) (was open)`},
		{Level: "info", Msg: `Resiliency circuit breaker "registryCB" closed for app flaky-app (persons/1) (was half-open)`},
	}, c.lines(t, false))
}

func TestPolicyLogTargets(t *testing.T) {
	const retryLine = `Resiliency retry policy "threeTimes" triggered for %s: attempt 1 failed, retrying in 5ms`

	tests := map[string]struct {
		def func(r *Resiliency) *PolicyDefinition
		// wantLine is the first line logged when the operation fails once (or,
		// for timeout-only policies, times out).
		wantLine string
		cbTarget string
	}{
		"app endpoint with method": {
			def: func(r *Resiliency) *PolicyDefinition {
				return r.EndpointPolicy("party-registry", "party-registry:persons/1")
			},
			wantLine: fmt.Sprintf(retryLine, "app party-registry (persons/1)"),
		},
		"app endpoint with HTTP path": {
			def: func(r *Resiliency) *PolicyDefinition {
				return r.EndpointPolicy("party-registry", "party-registry/v1/persons")
			},
			wantLine: fmt.Sprintf(retryLine, "app party-registry (v1/persons)"),
		},
		"app endpoint without method": {
			def:      func(r *Resiliency) *PolicyDefinition { return r.EndpointPolicy("slow-app", "slow-app") },
			wantLine: `Resiliency timeout policy "downstream" (50ms) triggered for app slow-app`,
		},
		"actor pre-lock": {
			def:      func(r *Resiliency) *PolicyDefinition { return r.ActorPreLockPolicy("myactor", "a1") },
			wantLine: fmt.Sprintf(retryLine, "actor myactor (id a1)"),
			cbTarget: "actor myactor (id a1)",
		},
		"actor post-lock": {
			def:      func(r *Resiliency) *PolicyDefinition { return r.ActorPostLockPolicy("myactor", "a1") },
			wantLine: `Resiliency timeout policy "downstream" (50ms) triggered for actor myactor (id a1)`,
		},
		"component outbound": {
			def:      func(r *Resiliency) *PolicyDefinition { return r.ComponentOutboundPolicy("statestore", Statestore) },
			wantLine: fmt.Sprintf(retryLine, "component statestore (outbound)"),
		},
		"component inbound": {
			def:      func(r *Resiliency) *PolicyDefinition { return r.ComponentInboundPolicy("statestore", Pubsub) },
			wantLine: fmt.Sprintf(retryLine, "component statestore (inbound)"),
		},
		"built-in": {
			def: func(r *Resiliency) *PolicyDefinition { return r.BuiltInPolicy(BuiltInServiceRetries) },
			wantLine: `Resiliency retry policy "DaprBuiltInServiceRetries" triggered for built-in operation: ` +
				`attempt 1 failed, retrying in 1s`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			l, c := newCapturingLogger(t)
			def := tc.def(newLogTestResiliency(t, l))

			var calls atomic.Int32
			_, _ = NewRunner[any](t.Context(), def)(func(ctx context.Context) (any, error) {
				if def.r == nil {
					// Timeout-only policy: block until the timeout fires.
					<-ctx.Done()
					return nil, ctx.Err()
				}
				if calls.Add(1) == 1 {
					return nil, errors.New("boom")
				}
				return nil, nil
			})

			lines := c.lines(t, false)
			require.NotEmpty(t, lines)
			assert.Equal(t, logLine{Level: "warning", Msg: tc.wantLine}, lines[0])

			if tc.cbTarget != "" {
				require.NotNil(t, def.cb)
				assert.Equal(t, "registryCB", def.cb.PolicyName)
				assert.Equal(t, tc.cbTarget, def.cb.Target)
			}
		})
	}
}
