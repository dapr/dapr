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

package framework

import "io"

func WithBinPath(binPath string) RunDaprdOption {
	return func(o *daprdOptions) {
		o.binPath = binPath
	}
}

func WithStdout(stdout io.WriteCloser) RunDaprdOption {
	return func(o *daprdOptions) {
		o.stdout = stdout
	}
}

func WithStderr(stderr io.WriteCloser) RunDaprdOption {
	return func(o *daprdOptions) {
		o.stderr = stderr
	}
}

func WithAppID(appID string) RunDaprdOption {
	return func(o *daprdOptions) {
		o.appID = appID
	}
}

func WithAppPort(port int) RunDaprdOption {
	return func(o *daprdOptions) {
		o.appPort = port
	}
}

func WithGRPCPort(port int) RunDaprdOption {
	return func(o *daprdOptions) {
		o.grpcPort = port
	}
}

func WithHTTPPort(port int) RunDaprdOption {
	return func(o *daprdOptions) {
		o.httpPort = port
	}
}

func WithInternalGRPCPort(port int) RunDaprdOption {
	return func(o *daprdOptions) {
		o.internalGRPCPort = port
	}
}

func WithPublicPort(port int) RunDaprdOption {
	return func(o *daprdOptions) {
		o.publicPort = port
	}
}

func WithMetricsPort(port int) RunDaprdOption {
	return func(o *daprdOptions) {
		o.metricsPort = port
	}
}

func WithProfilePort(port int) RunDaprdOption {
	return func(o *daprdOptions) {
		o.profilePort = port
	}
}

func WithRunError(ferr func(error)) RunDaprdOption {
	return func(o *daprdOptions) {
		o.runErrorFn = ferr
	}
}

func WithExitCode(code int) RunDaprdOption {
	return func(o *daprdOptions) {
		o.exitCode = code
	}
}

func WithAppHealthCheck(enabled bool) RunDaprdOption {
	return func(o *daprdOptions) {
		o.appHealthCheck = enabled
	}
}

func WithAppHealthCheckPath(path string) RunDaprdOption {
	return func(o *daprdOptions) {
		o.appHealthCheckPath = path
	}
}

func WithAppHealthProbeInterval(interval int) RunDaprdOption {
	return func(o *daprdOptions) {
		o.appHealthProbeInterval = interval
	}
}

func WithAppHealthProbeThreshold(threshold int) RunDaprdOption {
	return func(o *daprdOptions) {
		o.appHealthProbeThreshold = threshold
	}
}
