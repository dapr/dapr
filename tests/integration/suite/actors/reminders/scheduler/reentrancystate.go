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

package scheduler

import (
	"context"
	"fmt"
	gohttp "net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(reentrancystate))
}

const (
	reentrancystateType  = "widgetactor"
	reentrancystateID    = "widget1"
	reentrancystatePath  = "/v1.0/actors/" + reentrancystateType + "/" + reentrancystateID
	reentrancystateState = reentrancystatePath + "/state"
)

// reentrancystate mirrors dapr/dapr#10532: with reentrancy enabled, a reminder
// callback does a read-modify-write of a state key which already has completed
// writes behind it. The runtime persists that write exactly as it does for an
// ordinary method call. The reminder and timer callbacks also carry the
// Dapr-Reentrancy-Id header, like a method invocation does.
type reentrancystate struct {
	app *actors.Actors

	reminderIDs slice.Slice[string]
	timerIDs    slice.Slice[string]
}

func (r *reentrancystate) Setup(t *testing.T) []framework.Option {
	r.reminderIDs = slice.New[string]()
	r.timerIDs = slice.New[string]()

	r.app = actors.New(t,
		actors.WithActorTypes(reentrancystateType),
		actors.WithReentry(true),
		actors.WithActorTypeHandler(reentrancystateType, func(_ gohttp.ResponseWriter, req *gohttp.Request) {
			if req.Method == gohttp.MethodDelete {
				return
			}

			ctx := req.Context()
			switch {
			case strings.HasSuffix(req.URL.Path, "/method/foo"):
				// Give "widget" two completed writes before any reminder fires.
				r.app.Daprd().HTTPPost2xx(t, ctx, reentrancystateState, strings.NewReader(`[{"operation":"upsert","request":{"key":"widget","value":1}}]`))
				r.app.Daprd().HTTPPost2xx(t, ctx, reentrancystateState, strings.NewReader(`[{"operation":"upsert","request":{"key":"widget","value":2}}]`))

			case strings.HasSuffix(req.URL.Path, "/method/remind/tick"):
				r.reminderIDs.Append(req.Header.Get("Dapr-Reentrancy-Id"))
				r.app.Daprd().HTTPPost2xx(t, ctx, reentrancystateState, strings.NewReader(`[{"operation":"upsert","request":{"key":"canary","value":true}}]`))
				counter, err := strconv.Atoi(r.app.Daprd().ActorStateGet(t, ctx, reentrancystateType, reentrancystateID, "widget"))
				assert.NoError(t, err)
				r.app.Daprd().HTTPPost2xx(t, ctx, reentrancystateState, strings.NewReader(
					fmt.Sprintf(`[{"operation":"upsert","request":{"key":"widget","value":%d}}]`, counter+1)))

			case strings.HasSuffix(req.URL.Path, "/method/timer/tock"):
				r.timerIDs.Append(req.Header.Get("Dapr-Reentrancy-Id"))
			}
		}),
	)

	return []framework.Option{
		framework.WithProcesses(r.app),
	}
}

func (r *reentrancystate) Run(t *testing.T, ctx context.Context) {
	r.app.WaitUntilRunning(t, ctx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		r.app.Daprd().HTTPPost2xx(c, ctx, reentrancystatePath+"/method/foo", nil)
	}, time.Second*10, time.Millisecond*10, "actor not ready in time")

	require.Equal(t, "2", r.app.Daprd().ActorStateGet(t, ctx, reentrancystateType, reentrancystateID, "widget"))

	r.app.Daprd().HTTPPost2xx(t, ctx, reentrancystatePath+"/reminders/tick", strings.NewReader(`{"dueTime":"0s","period":"1s"}`))
	r.app.Daprd().HTTPPost2xx(t, ctx, reentrancystatePath+"/timers/tock", strings.NewReader(`{"dueTime":"0s","period":"1s"}`))

	// Every firing reads "widget" and advances it by one. Reaching 4 means two
	// callback writes persisted and were read back by the next callback.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		counter, err := strconv.Atoi(r.app.Daprd().ActorStateGet(c, ctx, reentrancystateType, reentrancystateID, "widget"))
		assert.NoError(c, err)
		assert.GreaterOrEqual(c, counter, 4)
	}, time.Second*20, time.Millisecond*10)

	assert.Equal(t, "true", r.app.Daprd().ActorStateGet(t, ctx, reentrancystateType, reentrancystateID, "canary"))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NotEmpty(c, r.timerIDs.Slice())
	}, time.Second*10, time.Millisecond*10)

	for _, id := range append(r.reminderIDs.Slice(), r.timerIDs.Slice()...) {
		assert.NotEmpty(t, id, "callback did not receive a reentrancy id")
	}
}
