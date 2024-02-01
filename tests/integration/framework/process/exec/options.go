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

package exec

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

type options struct {
	stdout io.WriteCloser
	stderr io.WriteCloser

	runErrorFn func(*testing.T, error)
	exitCode   int
	envs       map[string]string
}

func WithStdout(stdout io.WriteCloser) Option {
	return func(o *options) {
		o.stdout = stdout
	}
}

func WithStderr(stderr io.WriteCloser) Option {
	return func(o *options) {
		o.stderr = stderr
	}
}

func WithRunError(ferr func(*testing.T, error)) Option {
	return func(o *options) {
		o.runErrorFn = ferr
	}
}

func WithExitCode(code int) Option {
	return func(o *options) {
		o.exitCode = code
	}
}

// WithEnvVars sets the environment variables for the command. Expects a list
// of key value pairs.
func WithEnvVars(t *testing.T, envs ...string) Option {
	return func(o *options) {
		require.Equal(t, 0, len(envs)%2, "envs must be a list of key value pairs")
		if o.envs == nil {
			o.envs = make(map[string]string)
		}
		for i := 0; i < len(envs); i += 2 {
			o.envs[envs[i]] = envs[i+1]
		}
	}
}
