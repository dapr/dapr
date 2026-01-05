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

package scheduler

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(workers))
}

type workers struct {
	def        *scheduler.Scheduler
	deflogline *logline.LogLine

	custom        *scheduler.Scheduler
	customlogline *logline.LogLine
}

func (w *workers) Setup(t *testing.T) []framework.Option {
	uuid1, err := uuid.NewRandom()
	require.NoError(t, err)

	uuid2, err := uuid.NewRandom()
	require.NoError(t, err)

	w.deflogline = logline.New(t, logline.WithStdoutLineContains(
		fmt.Sprintf(`Starting queue with workers","id":"%s","count":2048}`, uuid1.String()),
	))
	w.customlogline = logline.New(t, logline.WithStdoutLineContains(
		fmt.Sprintf(`Starting queue with workers","id":"%s","count":256}`, uuid2.String()),
	))

	w.def = scheduler.New(t,
		scheduler.WithLogLineStderr(w.deflogline),
		scheduler.WithID(uuid1.String()),
		scheduler.WithWorkers(nil),
	)
	w.custom = scheduler.New(t,
		scheduler.WithWorkers(ptr.Of(uint32(256))),
		scheduler.WithLogLineStderr(w.customlogline),
		scheduler.WithID(uuid2.String()),
	)

	return []framework.Option{
		framework.WithProcesses(
			w.deflogline, w.customlogline,
			w.def, w.custom,
		),
	}
}

func (w *workers) Run(t *testing.T, ctx context.Context) {
	w.deflogline.EventuallyFoundAll(t)
	w.customlogline.EventuallyFoundAll(t)
}
