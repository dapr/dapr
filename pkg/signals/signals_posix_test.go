//go:build !windows
// +build !windows

/*
Copyright 2023 The Dapr Authors
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

package signals

// Note this file is not built on Windows, as we depend on syscall methods not available on Windows.

import (
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestContext(t *testing.T) {
	signal.Reset()

	t.Run("if receive signal, should cancel context", func(t *testing.T) {
		defer signal.Reset()
		onlyOneSignalHandler = make(chan struct{})

		ctx := Context()
		assert.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGINT))
		select {
		case <-ctx.Done():
		case <-time.After(1 * time.Second):
			t.Error("context should be cancelled in time")
		}
	})
}
