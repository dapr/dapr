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

package operator

import (
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
)

// options contains the options for running Sentry in integration tests.
type options struct {
	execOpts []exec.Option

	logLevel              string
	namespace             *string
	port                  int
	healthzPort           int
	metricsPort           int
	kubeconfigPath        *string
	configPath            *string
	disableLeaderElection bool
	trustAnchorsFile      *string
}

// Option is a function that configures the process.
type Option func(*options)

func WithExecOptions(execOptions ...exec.Option) Option {
	return func(o *options) {
		o.execOpts = execOptions
	}
}

func WithLogLevel(level string) Option {
	return func(o *options) {
		o.logLevel = level
	}
}

func WithAPIPort(port int) Option {
	return func(o *options) {
		o.port = port
	}
}

func WithHealthzPort(port int) Option {
	return func(o *options) {
		o.healthzPort = port
	}
}

func WithMetricsPort(port int) Option {
	return func(o *options) {
		o.metricsPort = port
	}
}

func WithKubeconfigPath(path string) Option {
	return func(o *options) {
		o.kubeconfigPath = &path
	}
}

func WithConfigPath(path string) Option {
	return func(o *options) {
		o.configPath = &path
	}
}

func WithDisableLeaderElection(disable bool) Option {
	return func(o *options) {
		o.disableLeaderElection = disable
	}
}

func WithTrustAnchorsFile(path string) Option {
	return func(o *options) {
		o.trustAnchorsFile = &path
	}
}

func WithNamespace(namespace string) Option {
	return func(o *options) {
		o.namespace = &namespace
	}
}
