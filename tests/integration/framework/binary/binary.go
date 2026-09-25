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

var buildTags = []string{
	"stablecomponents",
	"state_etcd",
	"crypto_localstorage",
	"middleware_http_routeralias",
	"conversation_echo",
	// state_spiffeprobe compiles in an integration-test-only state store that
	// reports whether the SPIFFE identity reached the component operation
	// context. Never set for released daprd flavors.
	"state_spiffeprobe",
	// secretstores_spiffeprobe compiles in an integration-test-only secret
	// store that reports whether the SPIFFE identity reached the context that
	// secretKeyRef entries are resolved on. Never set for released daprd
	// flavors.
	"secretstores_spiffeprobe",
	// bindings_metadataprobe compiles in an integration-test-only output
	// binding that echoes the request metadata it receives, letting a test
	// assert which metadata daprd forwards to a component. Never set for
	// released daprd flavors.
	"bindings_metadataprobe",
}

func BuildAll(t *testing.T) {
	t.Helper()

	binaryNames := []string{"daprd", "placement", "sentry", "operator", "injector", "scheduler"}
	helperBinaryNames := []string{"helmtemplate", "mcpstdioserver", "controllergen"}

	var wg sync.WaitGroup
	wg.Add(len(binaryNames))
	wg.Add(len(helperBinaryNames))
	rootDir := RootDir(t)
	for _, name := range binaryNames {
		if runtime.GOOS == "windows" {
			build(t, name, options{
				dir:  rootDir,
				tags: buildTags,
			})
			wg.Done()
		} else {
			go func(name string) {
				defer wg.Done()
				build(t, name, options{
					dir:  rootDir,
					tags: buildTags,
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

	generateCRDs(t)
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
		iowriter.Eventf(t, "%s not set, building %q binary", EnvKey(name), name)

		binPath := filepath.Join(tmpDir(), "dapr_integration_tests/"+name)
		if runtime.GOOS == "windows" {
			binPath += ".exe"
		}

		// Both streams share one writer so the build appears as a single block.
		iow := iowriter.New(t, name)

		iowriter.Eventf(t, "compiling %q from %q to %q", name, opts.dir, binPath)

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
		cmd.Stdout = iow
		cmd.Stderr = iow
		// Ensure CGO is disabled to avoid linking against system libraries.
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		assert.NoError(t, cmd.Run())

		assert.NoError(t, iow.Close())

		// TODO: @joshvanl: check if we can use `t.Setenv`
		//nolint:usetesting
		assert.NoError(t, os.Setenv(EnvKey(name), binPath))
	} else {
		iowriter.Eventf(t, "%s set, using pre-built binary %q", EnvKey(name), EnvValue(name))
	}
}

func EnvValue(name string) string {
	return os.Getenv(EnvKey(name))
}

func EnvKey(name string) string {
	return fmt.Sprintf("DAPR_INTEGRATION_%s_PATH", strings.ToUpper(name))
}

func tmpDir() string {
	if runtime.GOOS == "darwin" {
		return "/tmp"
	}
	return os.TempDir()
}
