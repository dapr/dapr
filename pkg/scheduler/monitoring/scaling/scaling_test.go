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

package scaling

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/diagridio/go-etcd-cron/api"
)

type source func() *api.QueueState

func (s source) GetQueueState() *api.QueueState { return s() }

func scrape(t *testing.T, c *Collector) string {
	t.Helper()

	rec := httptest.NewRecorder()
	c.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, Path, nil))
	require.Equal(t, http.StatusOK, rec.Code)
	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	return string(body)
}

func Test_Collector(t *testing.T) {
	t.Parallel()

	t.Run("no source serves no metrics", func(t *testing.T) {
		t.Parallel()

		assert.Empty(t, scrape(t, New()))
	})

	t.Run("serves only the two gauges, per namespace and app", func(t *testing.T) {
		t.Parallel()

		c := New()
		c.SetSource(source(func() *api.QueueState {
			return &api.QueueState{
				InFlight: map[string]map[string]uint64{
					"ns1": {"app1": 3, "app2": 1},
				},
				Staged: map[string]map[string]uint64{
					"ns2": {"app3": 7},
				},
			}
		}))

		assert.Equal(t, `# HELP dapr_scheduler_inflight_jobs Jobs triggered and not yet answered: waiting in a concurrency gate, or sent to a sidecar.
# TYPE dapr_scheduler_inflight_jobs gauge
dapr_scheduler_inflight_jobs{app_id="app1",namespace="ns1"} 3
dapr_scheduler_inflight_jobs{app_id="app2",namespace="ns1"} 1
# HELP dapr_scheduler_undeliverable_jobs Jobs parked because no connected sidecar hosts them.
# TYPE dapr_scheduler_undeliverable_jobs gauge
dapr_scheduler_undeliverable_jobs{app_id="app3",namespace="ns2"} 7
`, scrape(t, c))
	})

	t.Run("follows the latest source", func(t *testing.T) {
		t.Parallel()

		c := New()
		c.SetSource(source(func() *api.QueueState {
			return &api.QueueState{Staged: map[string]map[string]uint64{"ns1": {"old": 1}}}
		}))
		c.SetSource(source(func() *api.QueueState {
			return &api.QueueState{Staged: map[string]map[string]uint64{"ns1": {"new": 2}}}
		}))

		body := scrape(t, c)
		assert.Contains(t, body, `dapr_scheduler_undeliverable_jobs{app_id="new",namespace="ns1"} 2`)
		assert.NotContains(t, body, `app_id="old"`)
	})

	t.Run("an empty state serves no samples", func(t *testing.T) {
		t.Parallel()

		c := New()
		c.SetSource(source(func() *api.QueueState { return new(api.QueueState) }))
		assert.Empty(t, scrape(t, c))
	})
}
