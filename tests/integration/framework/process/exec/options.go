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
)

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
