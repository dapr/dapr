/*
Copyright 2025 The Dapr Authors
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

package lock

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_Lock(t *testing.T) {
	tests := map[string]struct {
		name        string
		action      func(l *Lock) error
		expectError bool
	}{
		"Successful Lock": {
			action: func(l *Lock) error {
				return l.Lock(context.Background())
			},
			expectError: false,
		},
		"Lock with Context Timeout": {
			action: func(l *Lock) error {
				l.Lock(context.Background())
				ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*50)
				defer cancel()
				return l.Lock(ctx)
			},
			expectError: true,
		},
		"Successful RLock": {
			action: func(l *Lock) error {
				return l.RLock(context.Background())
			},
			expectError: false,
		},
		"RLock with Context Timeout": {
			action: func(l *Lock) error {
				l.Lock(context.Background())
				ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*50)
				defer cancel()
				return l.RLock(ctx)
			},
			expectError: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			l := New()

			done := make(chan error)
			go func() {
				done <- test.action(l)
			}()

			select {
			case err := <-done:
				assert.Equal(t, (err != nil), test.expectError, "unexpected error, expected error: %v, got: %v", test.expectError, err)
			case <-time.After(time.Second):
				t.Errorf("test timed out")
			}
		})
	}
}
