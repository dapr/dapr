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
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/suite"
	_ "github.com/dapr/dapr/tests/integration/suite/healthz"
	_ "github.com/dapr/dapr/tests/integration/suite/ports"
)

const (
	defaultConcurrency = 3

	envConcurrency = "DAPR_INTEGRATION_CONCURRENCY"
	envDaprdPath   = "DAPR_INTEGRATION_DAPRD_PATH"
)

func RunIntegrationTests(t *testing.T) {
	// Parallelise the integration tests, but don't run more than `conc` (default
	// 3) at once.
	conc := concurrency(t)
	t.Logf("running integration tests with concurrency: %d", conc)

	buildDaprd(t)

	guard := make(chan struct{}, conc)

	for _, tcase := range suite.All() {
		tcase := tcase
		t.Run(reflect.TypeOf(tcase).Elem().Name(), func(t *testing.T) {
			t.Parallel()

			guard <- struct{}{}
			t.Cleanup(func() {
				<-guard
			})

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			t.Log("setting up test case")
			options := tcase.Setup(t, ctx)

			t.Log("running daprd")
			daprd := framework.RunDaprd(t, ctx, options...)

			t.Log("running test case")
			tcase.Run(t, ctx, daprd)

			t.Log("cleaning up test case")
			daprd.Cleanup(t)

			t.Log("done")
		})
	}
}

func concurrency(t *testing.T) int {
	conc := defaultConcurrency
	concS, ok := os.LookupEnv(envConcurrency)
	if ok {
		var err error
		conc, err = strconv.Atoi(concS)
		if err != nil {
			t.Fatalf("failed to parse %q: %s", envConcurrency, err)
		}
		if conc < 1 {
			t.Fatalf("%q must be >= 1", envConcurrency)
		}
	}

	return conc
}

func buildDaprd(t *testing.T) {
	if _, ok := os.LookupEnv(envDaprdPath); !ok {
		t.Logf("%q not set, building daprd binary", envDaprdPath)

		_, tfile, _, ok := runtime.Caller(0)
		require.True(t, ok)
		rootDir := filepath.Join(filepath.Dir(tfile), "../..")

		// Use a consistent temp dir for the binary so that the binary is cached on
		// subsequent runs.
		daprdPath := filepath.Join(os.TempDir(), "dapr_integration_tests/daprd")
		if runtime.GOOS == "windows" {
			daprdPath += ".exe"
		}

		// Ensure CGO is disabled to avoid linking against system libraries.
		t.Setenv("CGO_ENABLED", "0")

		t.Logf("Root dir: %q", rootDir)
		t.Logf("Building daprd binary to: %q", daprdPath)
		cmd := exec.Command("go", "build", "-tags=all_components", "-v", "-o", daprdPath, filepath.Join(rootDir, "cmd/daprd"))
		cmd.Dir = rootDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		require.NoError(t, cmd.Run())

		t.Setenv(envDaprdPath, daprdPath)
	}
}
