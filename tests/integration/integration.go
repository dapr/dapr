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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/suite"
	_ "github.com/dapr/dapr/tests/integration/suite/healthz"
	_ "github.com/dapr/dapr/tests/integration/suite/metadata"
	_ "github.com/dapr/dapr/tests/integration/suite/ports"
)

func RunIntegrationTests(t *testing.T) {
	buildBinaries(t)

	for _, tcase := range suite.All() {
		tcase := tcase
		tof := reflect.TypeOf(tcase).Elem()
		testName := filepath.Base(tof.PkgPath()) + "/" + tof.Name()

		t.Run(testName, func(t *testing.T) {
			t.Logf("%s: setting up test case", testName)
			options := tcase.Setup(t)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			t.Log("running framework")
			f := framework.Run(t, ctx, options...)

			t.Log("running test case")
			tcase.Run(t, ctx)

			t.Log("cleaning up framework")
			f.Cleanup(t)

			t.Log("done")
		})
	}
}

func buildBinaries(t *testing.T) {
	t.Helper()

	binaryNames := []string{"daprd", "placement"}

	var wg sync.WaitGroup
	wg.Add(len(binaryNames))
	for _, name := range binaryNames {
		go func(name string) {
			defer wg.Done()
			buildBinary(t, name)
		}(name)
	}
	wg.Wait()
}

func buildBinary(t *testing.T, name string) {
	t.Helper()
	env := fmt.Sprintf("DAPR_INTEGRATION_%s_PATH", strings.ToUpper(name))
	if _, ok := os.LookupEnv(env); !ok {
		t.Logf("%q not set, building %s binary", env, name)

		_, tfile, _, ok := runtime.Caller(0)
		require.True(t, ok)
		rootDir := filepath.Join(filepath.Dir(tfile), "../..")

		// Use a consistent temp dir for the binary so that the binary is cached on
		// subsequent runs.
		binPath := filepath.Join(os.TempDir(), "dapr_integration_tests/"+name)
		if runtime.GOOS == "windows" {
			binPath += ".exe"
		}

		// Ensure CGO is disabled to avoid linking against system libraries.
		os.Setenv("CGO_ENABLED", "0")

		t.Logf("Root dir: %q", rootDir)
		t.Logf("Compiling %q binary to: %q", name, binPath)
		cmd := exec.Command("go", "build", "-tags=allcomponents", "-v", "-o", binPath, filepath.Join(rootDir, "cmd/"+name))
		cmd.Dir = rootDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		require.NoError(t, cmd.Run())

		require.NoError(t, os.Setenv(env, binPath))
	}
}
