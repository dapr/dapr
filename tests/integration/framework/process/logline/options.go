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

package logline

// options contains the options for checking the log lines of a process.
type options struct {
	stdoutContains []string
	stderrContains []string
	// captureAll enables continuous log capture mode, where all logs are
	// captured to the buffer even without specific expected lines configured.
	// This is useful for tests that need to dynamically check log contents
	// using Contains() or Reset().
	captureAll bool
}

func WithStdoutLineContains(lines ...string) func(*options) {
	return func(o *options) {
		o.stdoutContains = append(o.stdoutContains, lines...)
	}
}

func WithStderrLineContains(lines ...string) func(*options) {
	return func(o *options) {
		o.stderrContains = append(o.stderrContains, lines...)
	}
}

// WithCaptureAll enables continuous log capture mode, where all logs are
// captured to the buffer even without specific expected lines configured.
// This is useful for tests that need to dynamically check log contents
// using Contains() or Reset().
func WithCaptureAll() func(*options) {
	return func(o *options) {
		o.captureAll = true
	}
}
