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
		if !strings.HasPrefix(l.Msg, "Resiliency retry (") &&
			!strings.HasPrefix(l.Msg, "Resiliency timeout (") &&
			!strings.HasPrefix(l.Msg, "Resiliency circuit breaker (") &&
			!strings.HasPrefix(l.Msg, "Error for operation ") {
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
					"slow-flaky-app": {
						Timeout: "downstream",
						Retry:   "threeTimes",
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
	const (
		op       = "endpoint[party-registry, party-registry:persons/1]"
		retrying = "Resiliency retry (threeTimes): error processing operation " + op + ". Retrying in 5ms…"
	)

	t.Run("first retry at warning, the rest at debug, then a recovery line", func(t *testing.T) {
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
			{Level: "warning", Msg: retrying},
			{Level: "info", Msg: "Resiliency retry (threeTimes): recovered processing operation " + op + " after 3 attempts"},
		}, c.lines(t, false))

		assert.Equal(t, []logLine{
			{Level: "warning", Msg: retrying},
			{Level: "debug", Msg: "Error for operation " + op + " was: boom"},
			{Level: "debug", Msg: retrying},
			{Level: "debug", Msg: "Error for operation " + op + " was: boom"},
			{Level: "info", Msg: "Resiliency retry (threeTimes): recovered processing operation " + op + " after 3 attempts"},
		}, c.lines(t, true))
	})

	t.Run("one warning line when retries are exhausted", func(t *testing.T) {
		l, c := newCapturingLogger(t)
		r := newLogTestResiliency(t, l)

		var calls atomic.Int32
		policy := NewRunner[any](t.Context(), r.EndpointPolicy("party-registry", "party-registry:persons/1"))
		_, err := policy(func(ctx context.Context) (any, error) {
			calls.Add(1)
			return nil, errors.New("boom")
		})
		require.Error(t, err)
		assert.Equal(t, int32(4), calls.Load())

		assert.Equal(t, []logLine{{Level: "warning", Msg: retrying}}, c.lines(t, false))

		var debugRetries int
		for _, line := range c.lines(t, true) {
			if line.Level == "debug" && line.Msg == retrying {
				debugRetries++
			}
		}
		assert.Equal(t, 2, debugRetries)
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
	t.Run("timeout is logged at warning", func(t *testing.T) {
		l, c := newCapturingLogger(t)
		r := newLogTestResiliency(t, l)

		policy := NewRunner[any](t.Context(), r.EndpointPolicy("slow-app", "slow-app:persons/1"))
		_, err := policy(func(ctx context.Context) (any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})
		require.ErrorIs(t, err, context.DeadlineExceeded)

		assert.Equal(t, []logLine{
			{Level: "warning", Msg: "Resiliency timeout (downstream): operation endpoint[slow-app, slow-app:persons/1] timed out after 50ms"},
		}, c.lines(t, false))
	})

	t.Run("first timeout at warning, the rest at debug", func(t *testing.T) {
		const (
			op       = "endpoint[slow-flaky-app, slow-flaky-app:persons/1]"
			timedOut = "Resiliency timeout (downstream): operation " + op + " timed out after 50ms"
			retrying = "Resiliency retry (threeTimes): error processing operation " + op + ". Retrying in 5ms…"
		)
		l, c := newCapturingLogger(t)
		r := newLogTestResiliency(t, l)

		var calls atomic.Int32
		policy := NewRunner[any](t.Context(), r.EndpointPolicy("slow-flaky-app", "slow-flaky-app:persons/1"))
		_, err := policy(func(ctx context.Context) (any, error) {
			calls.Add(1)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		require.Error(t, err)
		assert.Equal(t, int32(4), calls.Load())

		assert.Equal(t, []logLine{
			{Level: "warning", Msg: timedOut},
			{Level: "warning", Msg: retrying},
		}, c.lines(t, false))

		var debugTimeouts int
		for _, line := range c.lines(t, true) {
			if line.Level == "debug" && line.Msg == timedOut {
				debugTimeouts++
			}
		}
		assert.Equal(t, 3, debugTimeouts)
	})
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

	opened := logLine{Level: "warning", Msg: `Resiliency circuit breaker (registryCB): "flaky-app:persons/1" changed state from closed to open`}

	require.Error(t, run(true))
	require.Error(t, run(true))
	assert.Equal(t, []logLine{opened}, c.lines(t, false))

	// Once the breaker's timeout elapses, the next call is let through.
	assert.EventuallyWithT(t, func(ct *assert.CollectT) {
		assert.NoError(ct, run(false))
	}, 2*time.Second, 10*time.Millisecond)

	assert.Equal(t, []logLine{
		opened,
		{Level: "info", Msg: `Resiliency circuit breaker (registryCB): "flaky-app:persons/1" changed state from open to half-open`},
		{Level: "info", Msg: `Resiliency circuit breaker (registryCB): "flaky-app:persons/1" changed state from half-open to closed`},
	}, c.lines(t, false))
}

// TestPolicyLogPolicyNames checks that every kind of target logs the name of
// the policy from the Resiliency spec.
func TestPolicyLogPolicyNames(t *testing.T) {
	const retryLine = "Resiliency retry (threeTimes): error processing operation %s. Retrying in 5ms…"

	tests := map[string]struct {
		def func(r *Resiliency) *PolicyDefinition
		// wantLine is the first line logged when the operation fails once (or,
		// for timeout-only policies, times out).
		wantLine string
		wantCB   bool
	}{
		"app": {
			def: func(r *Resiliency) *PolicyDefinition {
				return r.EndpointPolicy("party-registry", "party-registry:persons/1")
			},
			wantLine: fmt.Sprintf(retryLine, "endpoint[party-registry, party-registry:persons/1]"),
		},
		"actor pre-lock": {
			def:      func(r *Resiliency) *PolicyDefinition { return r.ActorPreLockPolicy("myactor", "a1") },
			wantLine: fmt.Sprintf(retryLine, "actor[myactor, a1]"),
			wantCB:   true,
		},
		"actor post-lock": {
			def:      func(r *Resiliency) *PolicyDefinition { return r.ActorPostLockPolicy("myactor", "a1") },
			wantLine: "Resiliency timeout (downstream): operation actor[myactor, a1] timed out after 50ms",
		},
		"component outbound": {
			def:      func(r *Resiliency) *PolicyDefinition { return r.ComponentOutboundPolicy("statestore", Statestore) },
			wantLine: fmt.Sprintf(retryLine, "component[statestore] output"),
		},
		"component inbound": {
			def:      func(r *Resiliency) *PolicyDefinition { return r.ComponentInboundPolicy("statestore", Pubsub) },
			wantLine: fmt.Sprintf(retryLine, "component[statestore] input"),
		},
		"built-in": {
			def: func(r *Resiliency) *PolicyDefinition { return r.BuiltInPolicy(BuiltInServiceRetries) },
			wantLine: "Resiliency retry (DaprBuiltInServiceRetries): error processing operation " +
				"DaprBuiltInServiceRetries. Retrying in 1s…",
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

			if tc.wantCB {
				require.NotNil(t, def.cb)
				assert.Equal(t, "registryCB", def.cb.PolicyName)
			}
		})
	}
}
