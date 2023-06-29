//go:build perf
// +build perf

/*
Copyright 2023 The Dapr Authors
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

package utils

import (
	"log"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
)

type DaprMetrics struct {
	Baseline_latency       float64
	Dapr_latency           float64
	Added_latency          float64
	Sidecar_cpu            int64
	App_cpu                int64
	Sidecar_memory         float64
	App_memory             float64
	Application_throughput float64
}

// DAPR_PERF_METRICS_PROMETHEUS_URL needs to be set
func PushPrometheusMetrics(metrics DaprMetrics, building_block string, component string) {
	
	dapr_perf_metrics_prometheus_url := os.Getenv("DAPR_PERF_METRICS_PROMETHEUS_URL")
	if dapr_perf_metrics_prometheus_url == "" {
		return
	}

	// Create and register the dapr metrics required
	baselineLatencyGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "BASELINE_RESPONSE_TIME",
		Help: "Average Response Time of Baseline Test",
	})
	daprLatencyGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "DAPR_RESPONSE_TIME",
		Help: "Average Respone Time of Dapr Test",
	})
	addedLatencyGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "LATENCY_BY_DAPR",
		Help: "Added Latency by Dapr Sidecar",
	})
	appCpuGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "APP_CPU_USAGE",
		Help: "CPU Usage by app",
	})
	sidecarCpuGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "DAPR_SIDECAR_CPU_USAGE",
		Help: "CPU Usage by Dapr Sidecar",
	})
	appMemoryGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "APP_MEMORY_USAGE",
		Help: "Memory Usage by app",
	})
	sidecarMemoryGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "DAPR_SIDECAR_MEMORY_USAGE",
		Help: "Memory Usage by Dapr Sidecar",
	})
	applicationThroughputGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "APPLICATION_THROUGHPUT",
		Help: "Actual QPS",
	})

	// Create a pusher to push metrics to the Prometheus Pushgateway
	pusher := push.New(dapr_perf_metrics_prometheus_url, building_block).
		Collector(baselineLatencyGauge).
		Collector(daprLatencyGauge).
		Collector(addedLatencyGauge).
		Collector(appCpuGauge).
		Collector(sidecarCpuGauge).
		Collector(appMemoryGauge).
		Collector(sidecarMemoryGauge).
		Collector(applicationThroughputGauge).
		Grouping("building_block", building_block)

	// Add the component Grouping only if specified
	if len(component) > 0 {
		pusher.Grouping("component", component)
	}

	// Set the dapr_metrics values to the Gauges created
	baselineLatencyGauge.Set(metrics.Baseline_latency)
	daprLatencyGauge.Set(metrics.Dapr_latency)
	addedLatencyGauge.Set(metrics.Added_latency)
	appCpuGauge.Set(float64(metrics.App_cpu))
	sidecarCpuGauge.Set(float64(metrics.Sidecar_cpu))
	appMemoryGauge.Set(metrics.App_memory)
	sidecarMemoryGauge.Set(metrics.Sidecar_memory)
	applicationThroughputGauge.Set(metrics.Application_throughput)

	// Push the metrics value to the Pushgateway
	if err := pusher.Push(); err != nil {
		log.Println("Failed to push metrics:", err)
	}

}
