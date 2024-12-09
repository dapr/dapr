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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/iowriter"
)

type options struct {
	dir      string
	buildDir string
	tags     []string
}

func BuildAll(t *testing.T) {
	t.Helper()

	binaryNames := []string{"daprd", "placement", "sentry", "operator", "injector", "scheduler"}
	helperBinaryNames := []string{"helmtemplate"}

	var wg sync.WaitGroup
	wg.Add(len(binaryNames))
	wg.Add(len(helperBinaryNames))
	rootDir := RootDir(t)
	for _, name := range binaryNames {
		if runtime.GOOS == "windows" {
			build(t, name, options{
				dir:  rootDir,
				tags: []string{"allcomponents"},
			})
			wg.Done()
		} else {
			go func(name string) {
				defer wg.Done()
				build(t, name, options{
					dir:  rootDir,
					tags: []string{"allcomponents"},
				})
			}(name)
		}
	}

	helperRootDir := helperRootDir(t)
	for _, name := range helperBinaryNames {
		if runtime.GOOS == "windows" {
			build(t, name, options{
				dir:      helperRootDir,
				buildDir: name,
			})
			wg.Done()
		} else {
			go func(name string) {
				defer wg.Done()
				build(t, name, options{
					dir:      helperRootDir,
					buildDir: name,
				})
			}(name)
		}
	}
	wg.Wait()

	require.False(t, t.Failed())
}

func RootDir(t *testing.T) string {
	t.Helper()
	_, tFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(tFile), "../../../..")
}

func helperRootDir(t *testing.T) string {
	t.Helper()
	_, tFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(tFile), "./helpers")
}

func build(t *testing.T, name string, opts options) {
	t.Helper()

	if !assert.NotEmpty(t, opts.dir) {
		return
	}

	if _, ok := os.LookupEnv(EnvKey(name)); !ok {
		t.Logf("%q not set, building %q binary", EnvKey(name), name)

		// Use a consistent temp dir for the binary so that the binary is cached on
		// subsequent runs.
		var tmpdir string
		if runtime.GOOS == "darwin" {
			tmpdir = "/tmp"
		} else {
			tmpdir = os.TempDir()
		}
		binPath := filepath.Join(tmpdir, "dapr_integration_tests/"+name)
		if runtime.GOOS == "windows" {
			binPath += ".exe"
		}

		ioout := iowriter.New(t, name)
		ioerr := iowriter.New(t, name)

		t.Logf("Root dir: %q", opts.dir)
		t.Logf("Compiling %q binary to: %q", name, binPath)

		// get go build args
		goBuildArgs := []string{"build"}
		if len(opts.buildDir) > 0 {
			goBuildArgs = append(goBuildArgs, "-C", opts.buildDir)
		}
		if len(opts.tags) > 0 {
			goBuildArgs = append(goBuildArgs, "-tags="+strings.Join(opts.tags, ","))
		}
		goBuildArgs = append(goBuildArgs, "-v", "-o", binPath)
		if len(opts.buildDir) > 0 {
			goBuildArgs = append(goBuildArgs, ".")
		} else {
			goBuildArgs = append(goBuildArgs, "./cmd/"+name)
		}

		cmd := exec.Command("go", goBuildArgs...)
		cmd.Dir = opts.dir
		cmd.Stdout = ioout
		cmd.Stderr = ioerr
		// Ensure CGO is disabled to avoid linking against system libraries.
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		assert.NoError(t, cmd.Run())

		assert.NoError(t, ioout.Close())
		assert.NoError(t, ioerr.Close())

		assert.NoError(t, os.Setenv(EnvKey(name), binPath))
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
