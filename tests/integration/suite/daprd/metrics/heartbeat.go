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

package metrics

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(heartbeat))
}

type heartbeat struct {
	daprd *daprd.Daprd
	place *placement.Placement
}

func (h *heartbeat) Setup(t *testing.T) []framework.Option {
	h.place = placement.New(t)

	app := app.New(t,
		app.WithActorTypes("myactortype1", "myactortype2", "myactortype3"),
	)

	h.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(h.place.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
	)

	return []framework.Option{
		framework.WithProcesses(h.place, app, h.daprd),
	}
}

func (h *heartbeat) Run(t *testing.T, ctx context.Context) {
	h.place.WaitUntilRunning(t, ctx)
	h.daprd.WaitUntilRunning(t, ctx)

	// TODO: Fix these metrics actually adding the actor types, or remove the
	// `actor_type:` label all together.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp := h.daprd.Metrics(t, ctx)
		f := resp[fmt.Sprintf("dapr_runtime_actor_status_report_total|actor_type:|app_id:%s|operation:send", h.daprd.AppID())]
		assert.Equal(c, 2, int(f))
		for _, m := range []string{
			"dapr_runtime_actor_table_operation_recv_total|actor_type:|app_id:%s|operation:update",
			"dapr_runtime_actor_table_operation_recv_total|actor_type:|app_id:%s|operation:lock",
			"dapr_runtime_actor_table_operation_recv_total|actor_type:|app_id:%s|operation:unlock",
			"dapr_runtime_actor_table_operation_recv_total|actor_type:|app_id:%s|operation:lock",
			"dapr_runtime_actor_table_operation_recv_total|actor_type:|app_id:%s|operation:lock",
		} {
			f := resp[fmt.Sprintf(m, h.daprd.AppID())]
			assert.Equal(c, 1, int(f))
		}
	}, 10*time.Second, 10*time.Millisecond)
}
