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

package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(api))
}

type api struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	dataCh    chan []byte
}

func (a *api) Setup(t *testing.T) []framework.Option {
	a.scheduler = scheduler.New(t)

	a.dataCh = make(chan []byte, 1)
	app := app.New(t,
		app.WithHandlerFunc("/job/", func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			a.dataCh <- body
		}),
	)

	a.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(a.scheduler.Address()),
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
	)

	return []framework.Option{
		framework.WithProcesses(a.scheduler, app, a.daprd),
	}
}

func (a *api) Run(t *testing.T, ctx context.Context) {
	a.scheduler.WaitUntilRunning(t, ctx)
	a.daprd.WaitUntilRunning(t, ctx)

	tests := map[string]struct {
		data any
		exp  string
	}{
		"simple-string": {
			data: `"someData"`,
			exp:  `"someData"`,
		},
		"quoted-string": {
			data: `"\"xyz\""`,
			exp:  `"\"xyz\""`,
		},
		"number": {
			data: `123`,
			exp:  `123`,
		},
		"object": {
			data: `{"expression":"val"}`,
			exp:  `{"expression":"val"}`,
		},
		"object-space": {
			data: `  {    "expression":   "val" } `,
			exp:  `{"expression":"val"}`,
		},
		"proto-string": {
			data: `{"@type":"type.googleapis.com/google.protobuf.StringValue","value": "aproto"}`,
			exp:  `{"@type":"type.googleapis.com/google.protobuf.StringValue","value":"aproto"}`,
		},
		"proto-string-space": {
			data: `  {  "@type":  "type.googleapis.com/google.protobuf.StringValue","value": "aproto"   }   `,
			exp:  `{"@type":"type.googleapis.com/google.protobuf.StringValue", "value":"aproto"}`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Helper()

			body := strings.NewReader(fmt.Sprintf(`{"dueTime":"0s","data":%s}`, test.data))
			a.daprd.HTTPPost2xx(t, ctx, "/v1.0-alpha1/jobs/"+name, body)
			select {
			case data := <-a.dataCh:
				assert.JSONEq(t, test.exp, string(data))
			case <-time.After(time.Second * 10):
				assert.Fail(t, "timed out waiting for triggered job")
			}
		})
	}
}
