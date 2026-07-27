/*
Copyright 2026 The Dapr Authors
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

package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(etcd))
}

// etcd asserts that the embedded etcd server metrics are exposed on the
// scheduler metrics endpoint.
type etcd struct {
	scheduler *scheduler.Scheduler
}

func (e *etcd) Setup(t *testing.T) []framework.Option {
	e.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(e.scheduler),
	}
}

func (e *etcd) Run(t *testing.T, ctx context.Context) {
	e.scheduler.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := e.scheduler.Metrics(c, ctx).All()
		assert.Contains(c, metrics, "etcd_server_has_leader")
		assert.Contains(c, metrics, "etcd_mvcc_db_total_size_in_bytes")
	}, time.Second*30, time.Millisecond*100)
}
