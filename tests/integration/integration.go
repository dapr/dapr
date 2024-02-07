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
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/suite"
)

var focusF = flag.String("focus", ".*", "Focus on specific test cases. Accepts regex.")

func RunIntegrationTests(t *testing.T) {
	flag.Parse()

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
	for _, tcase := range suite.All(t) {
		// Continue rather than using `t.Skip` to reduce the noise in the test
		// output.
		if !focus.MatchString(tcase.Name()) {
			t.Logf("skipping test case due to focus %s", tcase.Name())
			continue
		}
		focusedTests = append(focusedTests, tcase)
	}

	startTime := time.Now()
	for _, tcase := range focusedTests {
		t.Run(tcase.Name(), func(t *testing.T) {
			t.Logf("setting up test case")
			options := tcase.Setup(t)

			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			t.Cleanup(cancel)

			framework.Run(t, ctx, options...)

			t.Run("run", func(t *testing.T) {
				t.Log("running test case")
				tcase.Run(t, ctx)
			})
		})
	}

	t.Logf("Total integration test execution time: %s", time.Since(startTime).Truncate(time.Millisecond*100))
}
