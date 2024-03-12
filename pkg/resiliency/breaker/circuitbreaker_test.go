/*
Copyright 2021 The Dapr Authors
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

package breaker_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/expr"
	"github.com/dapr/dapr/pkg/resiliency/breaker"
	"github.com/dapr/kit/logger"
)

func TestCircuitBreaker(t *testing.T) {
	t.Parallel()
	log := logger.NewLogger("test")

	var trip expr.Expr
	err := trip.DecodeString("consecutiveFailures > 2")
	require.NoError(t, err)

	cb := breaker.CircuitBreaker{
		Name:    "test",
		Trip:    &trip,
		Timeout: 100 * time.Millisecond,
	}
	assert.Equal(t, breaker.StateUnknown, cb.State())
	cb.Initialize(log)
	assert.Equal(t, breaker.StateClosed, cb.State())

	for i := 0; i < 3; i++ {
		cb.Execute(func() (any, error) {
			return nil, errors.New("test")
		})
	}

	res, err := cb.Execute(func() (any, error) {
		return "❌", nil
	})
	assert.Equal(t, breaker.StateOpen, cb.State())
	require.EqualError(t, err, "circuit breaker is open")
	assert.Nil(t, res)

	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, breaker.StateHalfOpen, cb.State())
	res, err = cb.Execute(func() (any, error) {
		return 42, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 42, res)
}
