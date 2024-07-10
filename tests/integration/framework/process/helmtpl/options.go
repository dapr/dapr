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

package helm

import "github.com/dapr/dapr/tests/integration/framework/process/exec"

// Option is a function that configures the process.
type Option func(*options)

// options contains the options for running Placement in integration tests.
type options struct {
	execOpts []exec.Option

	setValues       []string
	setStringValues []string
	setJsonValue    string
}

func (o options) getHelmArgs() (args []string) {
	for _, v := range o.setValues {
		args = append(args, "--set", v)
	}
	for _, v := range o.setStringValues {
		args = append(args, "--set-string", v)
	}

	if o.setJsonValue != "" {
		args = append(args, "--set-json", o.setJsonValue)
	}
	return args
}

func WithExecOptions(execOptions ...exec.Option) Option {
	return func(o *options) {
		o.execOpts = execOptions
	}
}

func WithValues(values ...string) Option {
	return func(o *options) {
		o.setValues = values
	}
}

func WithStringValues(values ...string) Option {
	return func(o *options) {
		o.setStringValues = values
	}
}

func WithJsonValue(jsonString string) Option {
	return func(o *options) {
		o.setJsonValue = jsonString
	}
}
