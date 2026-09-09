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

package cluster

import (
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
)

type Option func(*options)

type options struct {
	count uint32

	overrideBroadcastHostPorts []string
	schedulerOptions           []scheduler.Option
}

// WithSchedulerOptions appends options to every scheduler in the cluster.
func WithSchedulerOptions(opts ...scheduler.Option) Option {
	return func(o *options) {
		o.schedulerOptions = append(o.schedulerOptions, opts...)
	}
}

func WithCount(count uint32) Option {
	return func(o *options) {
		o.count = count
	}
}

func WithOverrideBroadcastHostPorts(addresses ...string) Option {
	return func(o *options) {
		o.overrideBroadcastHostPorts = append(o.overrideBroadcastHostPorts, addresses...)
	}
}
