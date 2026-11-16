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

// Package scaling serves the scheduler metrics used to scale an app's
// workers: how many of its jobs are in flight, and how many are undeliverable
// because no sidecar hosts them. They are served from a registry of their own
// so a scaler can scrape them without every other scheduler metric.
package scaling

import (
	"net/http"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/diagridio/go-etcd-cron/api"
)

// Path is the metrics server path the scaling metrics are served on.
const Path = "/metrics/scaling"

// Source reports the cron queue state of this scheduler.
type Source interface {
	GetQueueState() *api.QueueState
}

// Collector reports the queue state of the current source as gauges. Each
// scheduler replica reports only the jobs it owns, so a reader sums over
// replicas. The counts are held in memory by the cron, so after a restart or
// rebalance the undeliverable count can read 0 until overdue jobs fire again.
type Collector struct {
	source   atomic.Pointer[Source]
	inFlight *prometheus.Desc
	staged   *prometheus.Desc
}

func New() *Collector {
	labels := []string{"namespace", "app_id"}
	return &Collector{
		inFlight: prometheus.NewDesc(
			"dapr_scheduler_inflight_jobs",
			"Jobs triggered and not yet answered: waiting in a concurrency gate, or sent to a sidecar.",
			labels, nil,
		),
		staged: prometheus.NewDesc(
			"dapr_scheduler_undeliverable_jobs",
			"Jobs parked because no connected sidecar hosts them.",
			labels, nil,
		),
	}
}

// SetSource sets the cron the collector reads. The scheduler recreates its
// cron when it restarts the server, and the collector follows it.
func (c *Collector) SetSource(s Source) {
	c.source.Store(&s)
}

// Handler serves the scaling metrics, and only those.
func (c *Collector) Handler() http.Handler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(c)
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.inFlight
	ch <- c.staged
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	s := c.source.Load()
	if s == nil {
		return
	}

	state := (*s).GetQueueState()
	collect(ch, c.inFlight, state.InFlight)
	collect(ch, c.staged, state.Staged)
}

func collect(ch chan<- prometheus.Metric, desc *prometheus.Desc, counts map[string]map[string]uint64) {
	for namespace, apps := range counts {
		for appID, n := range apps {
			ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, float64(n), namespace, appID)
		}
	}
}
