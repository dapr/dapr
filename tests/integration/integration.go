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

package integration

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/framework/iowriter"
	"github.com/dapr/dapr/tests/integration/framework/progress"
	"github.com/dapr/dapr/tests/integration/suite"
)

// caseTimeout is the wall clock budget for a single test case, covering both
// process startup and the case body.
const caseTimeout = 45 * time.Second

var (
	focusF       = flag.String("focus", ".*", "Focus on specific test cases. Accepts regex.")
	parallelFlag = flag.Bool("integration-parallel", true, "Disable running integration tests in parallel")
)

func RunIntegrationTests(t *testing.T) {
	flag.Parse()

	focus, err := regexp.Compile(*focusF)
	require.NoError(t, err, "Invalid parameter focus")
	t.Logf("running test suite with focus: %s", *focusF)

	require.NoError(t, iowriter.ResetLogDir(), "could not prepare the log directory")

	_, err = iowriter.RedirectInProcessLogs()
	require.NoError(t, err, "could not redirect in-process logs")

	var binFailed bool
	t.Run("build binaries", func(t *testing.T) {
		t.Cleanup(func() {
			binFailed = t.Failed()
		})
		binary.BuildAll(t)
	})
	require.False(t, binFailed, "building binaries must succeed")

	focusedTests := make([]suite.NamedCase, 0)
	skippedTests := 0
	for _, tcase := range suite.All(t) {
		// Continue rather than using `t.Skip` to reduce the noise in the test output.
		if !focus.MatchString(tcase.Name()) {
			skippedTests++
			continue
		}
		focusedTests = append(focusedTests, tcase)
	}

	// Drawn on the terminal rather than through t.Log, so that a run shows it is
	// making progress without the test output having to be verbose.
	prog := progress.New(len(focusedTests))
	t.Cleanup(prog.Finish)

	startTime := time.Now()
	t.Cleanup(func() {
		executionMessage := fmt.Sprintf("Total integration test execution time for %d test cases: %s", len(focusedTests), time.Since(startTime).Truncate(time.Millisecond*100))
		t.Log(strings.Repeat("-", len(executionMessage)))
		if skippedTests > 0 {
			t.Logf("%d test cases were skipped due to focus", skippedTests)
		}
		t.Log(executionMessage)
		t.Log(strings.Repeat("-", len(executionMessage)))

		logFailures(t, len(focusedTests))
	})

	for _, tcase := range focusedTests {
		tcase.Case = reflect.New(reflect.TypeOf(tcase.Case).Elem()).Interface().(suite.Case)

		t.Run(tcase.Name(), func(t *testing.T) {
			if *parallelFlag {
				t.Parallel()
			}

			// Registered before anything else so that it runs last, once the
			// processes have been cleaned up and the result is final.
			t.Cleanup(func() { prog.Done(tcase.Name(), t.Failed()) })

			options := tcase.Setup(t)

			iowriter.Eventf(t, "setting up test case")

			// TODO: @joshvanl: update framework to use `t.Context()` which is
			// correctly respected on cleanup.

			ctx, cancel := context.WithTimeout(context.Background(), caseTimeout)
			t.Cleanup(cancel)

			// A blown deadline explains every downstream failure it causes, so
			// call it out at the top of the report rather than leaving the
			// reader to infer it.
			t.Cleanup(func() {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					iowriter.Notef(t, "test case exceeded its %s deadline", caseTimeout)
				}
			})

			framework.Run(t, ctx, options...)

			iowriter.Eventf(t, "running test case")
			tcase.Run(t, ctx)
		})
	}
}

// logFailures lists the failed test cases and where to read their logs. It runs
// last, so that the one thing worth acting on is the last thing on screen no
// matter which output format the run used.
func logFailures(t *testing.T, total int) {
	failures := iowriter.Failures()
	if len(failures) == 0 {
		return
	}

	width := 0
	for _, f := range failures {
		width = max(width, len(f.Test))
	}

	t.Logf("%d of %d test cases failed:", len(failures), total)
	for _, f := range failures {
		// Logs are inlined rather than written to a file on CI, where there is
		// nowhere useful to point at.
		if f.Path == "" {
			t.Logf("  %s", f.Test)
			continue
		}
		t.Logf("  %-*s  %s", width, f.Test, f.Path)
	}

	if failures[0].Path != "" {
		t.Logf("Read all of them with: less %s", filepath.Join(iowriter.LogDir(), "*.log"))
	}
}
