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

package once

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework/process"
)

// once ensures that a process is only run and cleaned up once.
type once struct {
	process.Interface
	runOnce   atomic.Bool
	cleanOnce atomic.Bool

	failRun     func(t *testing.T)
	failCleanup func(t *testing.T)
}

func Wrap(proc process.Interface) process.Interface {
	return &once{
		Interface: proc,
		failRun: func(t *testing.T) {
			t.Fatal("process has already been run")
		},
		failCleanup: func(t *testing.T) {
			t.Fatal("process has already been cleaned up")
		},
	}
}

func (o *once) Run(t *testing.T, ctx context.Context) {
	if !o.runOnce.CompareAndSwap(false, true) {
		o.failRun(t)
		return
	}

	o.Interface.Run(t, ctx)
}

func (o *once) Cleanup(t *testing.T) {
	if !o.cleanOnce.CompareAndSwap(false, true) {
		o.failCleanup(t)
		return
	}

	o.Interface.Cleanup(t)
}
