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
	gohttp "net/http"
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
	suite.Register(new(reentrancydisabled))
}

const (
	reentrancydisabledType = "widgetactor"
	reentrancydisabledID   = "widget1"
	reentrancydisabledPath = "/v1.0/actors/" + reentrancydisabledType + "/" + reentrancydisabledID
)

// reentrancydisabled is the control for reentrancystate: with reentrancy
// disabled, reminder and timer callbacks carry no Dapr-Reentrancy-Id.
type reentrancydisabled struct {
	app *actors.Actors

	reminderIDs slice.Slice[string]
	timerIDs    slice.Slice[string]
}

func (r *reentrancydisabled) Setup(t *testing.T) []framework.Option {
	r.reminderIDs = slice.New[string]()
	r.timerIDs = slice.New[string]()

	r.app = actors.New(t,
		actors.WithActorTypes(reentrancydisabledType),
		actors.WithReentry(false),
		actors.WithActorTypeHandler(reentrancydisabledType, func(_ gohttp.ResponseWriter, req *gohttp.Request) {
			switch {
			case strings.HasSuffix(req.URL.Path, "/method/remind/tick"):
				r.reminderIDs.Append(req.Header.Get("Dapr-Reentrancy-Id"))
			case strings.HasSuffix(req.URL.Path, "/method/timer/tock"):
				r.timerIDs.Append(req.Header.Get("Dapr-Reentrancy-Id"))
			}
		}),
	)

	return []framework.Option{
		framework.WithProcesses(r.app),
	}
}

func (r *reentrancydisabled) Run(t *testing.T, ctx context.Context) {
	r.app.WaitUntilRunning(t, ctx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		r.app.Daprd().HTTPPost2xx(c, ctx, reentrancydisabledPath+"/method/foo", nil)
	}, time.Second*10, time.Millisecond*10, "actor not ready in time")

	r.app.Daprd().HTTPPost2xx(t, ctx, reentrancydisabledPath+"/reminders/tick", strings.NewReader(`{"dueTime":"0s","period":"1s"}`))
	r.app.Daprd().HTTPPost2xx(t, ctx, reentrancydisabledPath+"/timers/tock", strings.NewReader(`{"dueTime":"0s","period":"1s"}`))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NotEmpty(c, r.reminderIDs.Slice())
		assert.NotEmpty(c, r.timerIDs.Slice())
	}, time.Second*20, time.Millisecond*10)

	for _, id := range append(r.reminderIDs.Slice(), r.timerIDs.Slice()...) {
		assert.Empty(t, id, "callback received a reentrancy id with reentrancy disabled")
	}
}
