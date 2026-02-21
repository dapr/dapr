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
	"flag"
	"fmt"
	"hash/fnv"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/suite"
)

var (
	focusF         = flag.String("focus", ".*", "Focus on specific test cases. Accepts regex.")
	partitionTotal = flag.Uint("partition-total", 1, "Total number of partitions to split the test suite into. Used in conjunction with `partition-index`.")
	partitionIndex = flag.Uint("partition-index", 0, "Index of the partition to run. Used in conjunction with `partition-total`.")
	parallelFlag   = flag.Bool("integration-parallel", true, "Disable running integration tests in parallel")
)

func RunIntegrationTests(t *testing.T) {
	flag.Parse()

	require.GreaterOrEqual(t, *partitionTotal, uint(1), "partition-total must be at least 1")
	require.Less(t, *partitionIndex, *partitionTotal, "partition-index must be less than partition-total")

	focus, err := regexp.Compile(*focusF)
	require.NoError(t, err, "Invalid parameter focus")
	t.Logf("running test suite with focus: %s", *focusF)

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
		// Continue rather than using `t.Skip` to reduce the noise in the test
		// output.
		if !focus.MatchString(tcase.Name()) {
			skippedTests++
			continue
		}

		if *partitionTotal > 1 {
			fnvHash := fnv.New32a()
			_, err = fnvHash.Write([]byte(tcase.Name()))
			require.NoError(t, err, "hashing test case name should not fail")
			hashValue := fnvHash.Sum32()
			if hashValue%uint32(*partitionTotal) != uint32(*partitionIndex) {
				skippedTests++
				continue
			}
		}
		focusedTests = append(focusedTests, tcase)
	}

	startTime := time.Now()
	t.Cleanup(func() {
		executionMessage := fmt.Sprintf("Total integration test execution time for %d test cases: %s", len(focusedTests), time.Since(startTime).Truncate(time.Millisecond*100))
		t.Log(strings.Repeat("-", len(executionMessage)))
		if skippedTests > 0 {
			t.Logf("%d test cases were skipped due to focus or test partition", skippedTests)
		}
		t.Log(executionMessage)
		t.Log(strings.Repeat("-", len(executionMessage)))
	})

	for _, tcase := range focusedTests {
		tcase.Case = reflect.New(reflect.TypeOf(tcase.Case).Elem()).Interface().(suite.Case)

		t.Run(tcase.Name(), func(t *testing.T) {
			if *parallelFlag {
				t.Parallel()
			}

			options := tcase.Setup(t)

			t.Log("setting up test case")

			// TODO: @joshvanl: update framework to use `t.Context()` which is
			// correctly respected on cleanup.
			//nolint:usetesting
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			t.Cleanup(cancel)

			framework.Run(t, ctx, options...)

			t.Run("run", func(t *testing.T) {
				t.Log("running test case")
				tcase.Run(t, ctx)
			})
		})
	}
}
