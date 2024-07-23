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
	helperBinaryNames := []string{"helmtemplate"}

	var wg sync.WaitGroup
	wg.Add(len(binaryNames))
	wg.Add(len(helperBinaryNames))
	binaryBuildOpts := withTags("allcomponents", "wfbackendsqlite")
	for _, name := range binaryNames {
		if runtime.GOOS == "windows" {
			Build(t, name, binaryBuildOpts)
			wg.Done()
		} else {
			go func(name string) {
				defer wg.Done()
				Build(t, name, binaryBuildOpts)
			}(name)
		}
	}
	helpOpts := withRootDirFunc(GetHelperRootDir)
	for _, name := range helperBinaryNames {
		if runtime.GOOS == "windows" {
			Build(t, name, helpOpts, withBuildDir(name))
			wg.Done()
		} else {
			go func(name string) {
				defer wg.Done()
				Build(t, name, helpOpts, withBuildDir(name))
			}(name)
		}
	}
	wg.Wait()
}

func GetRootDir(t *testing.T) string {
	t.Helper()
	_, tFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	dir, err := os.Getwd()
	require.NoError(t, err)
	t.Logf(">> DIR: %s\n", dir)

	fv := filepath.Join(filepath.Dir(tFile), "../../../..")
	t.Logf(">> FV: %s\n", dir)
	stat, err := os.Stat(fv)
	t.Logf(">> STAT: %v ERROR:%v\n", stat, err)
	if stat != nil {
		t.Logf(">> STAT NAME\n", stat.Name())
	}

	return filepath.Join(filepath.Dir(tFile), "../../../..")
}

func GetHelperRootDir(t *testing.T) string {
	t.Helper()
	_, tFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(tFile), "./helpers")
}

func Build(t *testing.T, name string, bopts ...func(*buildOpts)) {
	opts := buildOpts{getRootDirFunc: GetRootDir}
	for _, o := range bopts {
		o(&opts)
	}
	t.Helper()
	if _, ok := os.LookupEnv(EnvKey(name)); !ok {
		t.Logf("%q not set, building %q binary", EnvKey(name), name)

		rootDir := opts.getRootDirFunc(t)

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

		t.Logf("Root dir: %q", rootDir)
		t.Logf("Compiling %q binary to: %q", name, binPath)

		// get go build args
		goBuildArgs := []string{"build"}
		if opts.buildDir != "" {
			goBuildArgs = append(goBuildArgs, "-C", opts.buildDir)
		}
		if len(opts.tags) > 0 {
			goBuildArgs = append(goBuildArgs, "-tags="+strings.Join(opts.tags, ","))
		}
		goBuildArgs = append(goBuildArgs, "-v", "-o", binPath)
		if opts.buildDir != "" {
			goBuildArgs = append(goBuildArgs, ".")
		} else {
			goBuildArgs = append(goBuildArgs, "./cmd/"+name)
		}

		cmd := exec.Command("go", goBuildArgs...)
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
