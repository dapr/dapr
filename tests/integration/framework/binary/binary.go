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

	binaryNames := []string{"daprd", "placement", "sentry", "operator", "injector", "scheduler"}

	t.Logf("Building binaries: %v", binaryNames)

	var wg sync.WaitGroup
	wg.Add(len(binaryNames) * 2)
	for _, name := range binaryNames {
		go func(name string) {
			defer wg.Done()
			BuildLocal(t, name)
		}(name)
		go func(name string) {
			defer wg.Done()
			BuildPrevious(t, name)
		}(name)
	}
	wg.Wait()
}

func BuildLocal(t *testing.T, name string) {
	t.Helper()
	if _, ok := os.LookupEnv(EnvKey(name)); !ok {
		t.Logf("%q not set, building %q binary", EnvKey(name), name)

		_, tfile, _, ok := runtime.Caller(0)
		require.True(t, ok)
		rootDir := filepath.Join(filepath.Dir(tfile), "../../../..")

		// Use a consistent temp dir for the binary so that the binary is cached on
		// subsequent runs.
		var tmpdir string
		if runtime.GOOS == "darwin" {
			tmpdir = "/tmp"
		} else {
			tmpdir = os.TempDir()
		}

		binPath := filepath.Join(tmpdir, "dapr_integration_tests/"+name)
		buildBin(t, buildReq{
			name:          name,
			rootDir:       rootDir,
			mainLocation:  filepath.Join("cmd", name),
			outputBinPath: binPath,
		})
		require.NoError(t, os.Setenv(EnvKey(name), binPath))
	} else {
		t.Logf("%q set, using %q pre-built binary", EnvKey(name), EnvValue(name))
	}
}

func BuildPrevious(t *testing.T, name string) {
	t.Helper()

	_, tfile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	prevPath := filepath.Join(tfile, "../previous")
	dir, err := os.ReadDir(prevPath)
	require.NoError(t, err)

	var wg sync.WaitGroup
	defer wg.Wait()

	wg.Add(len(dir))
	for _, entry := range dir {
		go func(entry os.DirEntry) {
			defer wg.Done()

			t.Logf("Building previous binary: [%q] %q", entry.Name(), name)
			rootDir := filepath.Join(prevPath, entry.Name())
			buildBin(t, buildReq{
				name:          name,
				rootDir:       rootDir,
				mainLocation:  name,
				outputBinPath: filepath.Join(os.TempDir(), "dapr_integration_tests/"+name+"-"+entry.Name()),
			})
		}(entry)
	}
}

type buildReq struct {
	name          string
	rootDir       string
	mainLocation  string
	outputBinPath string
}

// Use a consistent temp dir for the binary so that the binary is cached on
// subsequent runs.
func buildBin(t *testing.T, req buildReq) {
	if runtime.GOOS == "windows" {
		req.outputBinPath += ".exe"
	}

	ioout := iowriter.New(t, req.name)
	ioerr := iowriter.New(t, req.name)

	t.Logf("Root dir: %q", req.rootDir)
	t.Logf("Compiling %q binary to: %q", req.name, req.outputBinPath)
	cmd := exec.Command("go", "build", "-tags=allcomponents,wfbackendsqlite", "-v", "-o", req.outputBinPath, "./"+req.mainLocation) // #nosec G204
	cmd.Dir = req.rootDir
	cmd.Stdout = ioout
	cmd.Stderr = ioerr
	// Ensure CGO is disabled to avoid linking against system libraries.
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	require.NoError(t, cmd.Run())

	require.NoError(t, ioout.Close())
	require.NoError(t, ioerr.Close())
}

func EnvValue(name string) string {
	return os.Getenv(EnvKey(name))
}

func EnvKey(name string) string {
	return fmt.Sprintf("DAPR_INTEGRATION_%s_PATH", strings.ToUpper(name))
}
