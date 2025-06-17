/*
Copyright 2025 The Dapr Authors
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

package embed

import (
	"context"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(flagerrors))
}

type flagerrors struct {
	scheduler1 *scheduler.Scheduler
	scheduler2 *scheduler.Scheduler
	logline1   *logline.LogLine
	logline2   *logline.LogLine
}

func (f *flagerrors) Setup(t *testing.T) []framework.Option {
	f.logline1 = logline.New(t, logline.WithStdoutLineContains(
		`cannot use --etcd-client-endpoints with --etcd-embed`,
	))
	f.logline2 = logline.New(t, logline.WithStdoutLineContains(
		`must specify --etcd-client-endpoints when not using embedded etcd`,
	))

	f.scheduler1 = scheduler.New(t,
		scheduler.WithEmbed(true),
		scheduler.WithClientEndpoints("localhost:1234"),
		scheduler.WithExit1(),
		scheduler.WithLogLineStdout(f.logline1),
	)
	f.scheduler2 = scheduler.New(t,
		scheduler.WithEmbed(false),
		scheduler.WithExit1(),
		scheduler.WithLogLineStdout(f.logline2),
	)

	return []framework.Option{
		framework.WithProcesses(f.logline1, f.logline2, f.scheduler1, f.scheduler2),
	}
}

func (f *flagerrors) Run(t *testing.T, ctx context.Context) {
	f.logline1.EventuallyFoundAll(t)
	f.logline2.EventuallyFoundAll(t)
}
