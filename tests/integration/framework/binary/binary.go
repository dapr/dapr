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

package binary

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/iowriter"
)

func BuildAll(t *testing.T) {
	t.Helper()

	binaryNames := []string{"daprd", "placement", "sentry", "operator"}

	var wg sync.WaitGroup
	wg.Add(len(binaryNames))
	for _, name := range binaryNames {
		if runtime.GOOS == "windows" {
			Build(t, name)
			wg.Done()
		} else {
			go func(name string) {
				defer wg.Done()
				Build(t, name)
			}(name)
		}
	}
	wg.Wait()
}

func Build(t *testing.T, name string) {
	t.Helper()
	if _, ok := os.LookupEnv(EnvKey(name)); !ok {
		t.Logf("%q not set, building %q binary", EnvKey(name), name)

		_, tfile, _, ok := runtime.Caller(0)
		require.True(t, ok)
		rootDir := filepath.Join(filepath.Dir(tfile), "../../../..")

		// Use a consistent temp dir for the binary so that the binary is cached on
		// subsequent runs.
		binPath := filepath.Join(os.TempDir(), "dapr_integration_tests/"+name)
		if runtime.GOOS == "windows" {
			binPath += ".exe"
		}

		ioout := iowriter.New(t, name)
		ioerr := iowriter.New(t, name)

		t.Logf("Root dir: %q", rootDir)
		t.Logf("Compiling %q binary to: %q", name, binPath)
		cmd := exec.Command("go", "build", "-tags=allcomponents,wfbackendsqlite", "-v", "-o", binPath, "./cmd/"+name)
		cmd.Dir = rootDir
		cmd.Stdout = ioout
		cmd.Stderr = ioerr
		// Ensure CGO is disabled to avoid linking against system libraries.
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		require.NoError(t, cmd.Run())

		require.NoError(t, ioout.Close())
		require.NoError(t, ioerr.Close())

		require.NoError(t, os.Setenv(EnvKey(name), binPath))
	} else {
		t.Logf("%q set, using %q pre-built binary", EnvKey(name), EnvValue(name))
	}
}

func EnvValue(name string) string {
	return os.Getenv(EnvKey(name))
}

func EnvKey(name string) string {
	return fmt.Sprintf("DAPR_INTEGRATION_%s_PATH", strings.ToUpper(name))
}
