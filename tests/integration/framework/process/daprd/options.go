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

package daprd

import "github.com/dapr/dapr/tests/integration/framework/process/exec"

// Option is a function that configures the dapr process.
type Option func(*options)

// options contains the options for running Daprd in integration tests.
type options struct {
	execOpts []exec.Option

	appID                   string
	appPort                 int
	grpcPort                int
	httpPort                int
	internalGRPCPort        int
	publicPort              int
	metricsPort             int
	profilePort             int
	appProtocol             string
	appHealthCheck          bool
	appHealthCheckPath      string
	appHealthProbeInterval  int
	appHealthProbeThreshold int
	resourceFiles           []string
	logLevel                string
}

func WithExecOptions(execOptions ...exec.Option) Option {
	return func(o *options) {
		o.execOpts = execOptions
	}
}

func WithAppID(appID string) Option {
	return func(o *options) {
		o.appID = appID
	}
}

func WithAppPort(port int) Option {
	return func(o *options) {
		o.appPort = port
	}
}

func WithAppProtocol(protocol string) Option {
	return func(o *options) {
		o.appProtocol = protocol
	}
}

func WithGRPCPort(port int) Option {
	return func(o *options) {
		o.grpcPort = port
	}
}

func WithHTTPPort(port int) Option {
	return func(o *options) {
		o.httpPort = port
	}
}

func WithInternalGRPCPort(port int) Option {
	return func(o *options) {
		o.internalGRPCPort = port
	}
}

func WithPublicPort(port int) Option {
	return func(o *options) {
		o.publicPort = port
	}
}

func WithMetricsPort(port int) Option {
	return func(o *options) {
		o.metricsPort = port
	}
}

func WithProfilePort(port int) Option {
	return func(o *options) {
		o.profilePort = port
	}
}

func WithAppHealthCheck(enabled bool) Option {
	return func(o *options) {
		o.appHealthCheck = enabled
	}
}

func WithAppHealthCheckPath(path string) Option {
	return func(o *options) {
		o.appHealthCheckPath = path
	}
}

func WithAppHealthProbeInterval(interval int) Option {
	return func(o *options) {
		o.appHealthProbeInterval = interval
	}
}

func WithAppHealthProbeThreshold(threshold int) Option {
	return func(o *options) {
		o.appHealthProbeThreshold = threshold
	}
}

func WithResourceFiles(files ...string) Option {
	return func(o *options) {
		o.resourceFiles = files
	}
}

func WithLogLevel(logLevel string) Option {
	return func(o *options) {
		o.logLevel = logLevel
	}
}
