/*
Copyright 2024 The Dapr Authors
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

package scheduler

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
)

type Option func(*options)

type options struct {
	execOpts []exec.Option

	id                       string
	etcdInitialCluster       *string
	etcdClientPort           int
	namespace                string
	etcdBackendBatchInterval string

	embed           *bool
	clientEndpoints *[]string
	clientUsername  *string
	clientPassword  *string

	logLevel    string
	port        int
	healthzPort int
	metricsPort int
	sentry      *sentry.Sentry
	dataDir     *string
	kubeconfig  *string
	mode        *string
	workers     *uint32

	overrideBroadcastHostPort *string
}

func WithExecOptions(execOptions ...exec.Option) Option {
	return func(o *options) {
		o.execOpts = append(o.execOpts, execOptions...)
	}
}

func WithPort(port int) Option {
	return func(o *options) {
		o.port = port
	}
}

func WithID(id string) Option {
	return func(o *options) {
		o.id = id
	}
}

// WithInitialCluster adds the initial etcd cluster peers. This should include http:// in the url.
func WithInitialCluster(initialCluster string) Option {
	return func(o *options) {
		o.etcdInitialCluster = &initialCluster
	}
}

func WithEtcdClientPort(port int) Option {
	return func(o *options) {
		o.etcdClientPort = port
	}
}

func WithLogLevel(level string) Option {
	return func(o *options) {
		o.logLevel = level
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

func WithSentry(sentry *sentry.Sentry) Option {
	return func(o *options) {
		o.sentry = sentry
	}
}

func WithNamespace(namespace string) Option {
	return func(o *options) {
		o.namespace = namespace
	}
}

func WithDataDir(dataDir string) Option {
	return func(o *options) {
		o.dataDir = &dataDir
	}
}

func WithKubeconfig(kubeconfig string) Option {
	return func(o *options) {
		o.kubeconfig = &kubeconfig
	}
}

func WithMode(mode string) Option {
	return func(o *options) {
		o.mode = &mode
	}
}

func WithOverrideBroadcastHostPort(address string) Option {
	return func(o *options) {
		o.overrideBroadcastHostPort = &address
	}
}

func WithEmbed(embed bool) Option {
	return func(o *options) {
		o.embed = &embed
	}
}

func WithClientEndpoints(endpoints ...string) Option {
	return func(o *options) {
		o.clientEndpoints = &endpoints
	}
}

func WithClientUsername(username string) Option {
	return func(o *options) {
		o.clientUsername = &username
	}
}

func WithClientPassword(password string) Option {
	return func(o *options) {
		o.clientPassword = &password
	}
}

func WithLogLineStdout(ll *logline.LogLine) Option {
	return WithExecOptions(exec.WithStdout(ll.Stdout()))
}

func WithLogLineStderr(ll *logline.LogLine) Option {
	return WithExecOptions(exec.WithStderr(ll.Stdout()))
}

func WithWorkers(workers *uint32) Option {
	return func(o *options) {
		o.workers = workers
	}
}

func WithExit1() Option {
	return WithExecOptions(
		exec.WithExitCode(1),
		exec.WithRunError(func(t *testing.T, err error) {
			assert.ErrorContains(t, err, "exit status 1")
		}),
	)
}
