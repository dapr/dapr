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

package executor

import (
	"bytes"
	"context"
	"regexp"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
)

// deactivateWorkerFrame matches a stack frame inside the closure New starts.
// The trailing paren excludes "created by" lines, which name no arguments.
var deactivateWorkerFrame = regexp.MustCompile(`executor\.New\.func\d+\(`)

func countDeactivateWorkers() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}

	var count int
	for g := range bytes.SplitSeq(buf, []byte("\n\n")) {
		if deactivateWorkerFrame.Match(g) {
			count++
		}
	}
	return count
}

// A factory is built on every workflow actor registration, so its
// deactivation worker must exit with the registration's context. Not
// parallel: the count covers every factory in the process.
func TestNew_deactivateWorkerExitsWithContext(t *testing.T) {
	baseline := countDeactivateWorkers()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	for range 5 {
		_, err := New(ctx, Options{
			Actors:    actorsfake.New(),
			ActorType: "dapr.internal.default.test.executor",
		})
		require.NoError(t, err)
	}
	require.Eventually(t, func() bool {
		return countDeactivateWorkers() == baseline+5
	}, time.Second*5, time.Millisecond*10, "the frame pattern must find the workers")

	cancel()
	assert.Eventually(t, func() bool {
		return countDeactivateWorkers() <= baseline
	}, time.Second*5, time.Millisecond*10)
}
