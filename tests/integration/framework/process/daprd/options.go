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

import (
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
)

// Option is a function that configures the dapr process.
type Option func(*options)

// options contains the options for running Daprd in integration tests.
type options struct {
	execOpts []exec.Option

	appID                   string
	namespace               *string
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
	resourceDirs            []string
	configs                 []string
	placementAddresses      []string
	logLevel                string
	mode                    string
	enableMTLS              bool
	sentryAddress           string
	controlPlaneAddress     string
	disableK8sSecretStore   *bool
	gracefulShutdownSeconds *int
	blockShutdownDuration   *string
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

func WithNamespace(namespace string) Option {
	return func(o *options) {
		o.namespace = &namespace
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
		o.resourceFiles = append(o.resourceFiles, files...)
	}
}

// WithInMemoryActorStateStore adds an in-memory state store component, which is also enabled as actor state store.
func WithInMemoryActorStateStore(storeName string) Option {
	return WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: ` + storeName + `
spec:
  type: state.in-memory
  version: v1
  metadata:
    - name: actorStateStore
      value: true
`)
}

func WithResourcesDir(dirs ...string) Option {
	return func(o *options) {
		o.resourceDirs = dirs
	}
}

func WithConfigs(configs ...string) Option {
	return func(o *options) {
		o.configs = configs
	}
}

func WithPlacementAddresses(addresses ...string) Option {
	return func(o *options) {
		o.placementAddresses = addresses
	}
}

func WithLogLevel(logLevel string) Option {
	return func(o *options) {
		o.logLevel = logLevel
	}
}

func WithMode(mode string) Option {
	return func(o *options) {
		o.mode = mode
	}
}

func WithEnableMTLS(enable bool) Option {
	return func(o *options) {
		o.enableMTLS = enable
	}
}

func WithSentryAddress(address string) Option {
	return func(o *options) {
		o.sentryAddress = address
	}
}

func WithControlPlaneAddress(address string) Option {
	return func(o *options) {
		o.controlPlaneAddress = address
	}
}

func WithDisableK8sSecretStore(disable bool) Option {
	return func(o *options) {
		o.disableK8sSecretStore = &disable
	}
}

func WithDaprGracefulShutdownSeconds(seconds int) Option {
	return func(o *options) {
		o.gracefulShutdownSeconds = &seconds
	}
}

func WithDaprBlockShutdownDuration(duration string) Option {
	return func(o *options) {
		o.blockShutdownDuration = &duration
	}
}
