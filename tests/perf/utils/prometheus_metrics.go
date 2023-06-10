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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
)

type DaprMetrics struct {
	Dapr_latency           float64
	Sidecar_cpu            int64
	Sidecar_memory         float64
	Application_throughput float64
}

func PrometheusMetrics(metrics DaprMetrics, building_block string, component string) {
	
	//Create and register the dapr metrics required

	daprLatencyGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "DAPR_LATENCY",
		Help: "Average Latency by Dapr Sidecar",
	})
	sidecarCpuGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "DAPR_SIDECAR_CPU_USAGE",
		Help: "CPU Usage by Dapr Sidecar",
	})
	sidecarMemoryGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "DAPR_SIDECAR_MEMORY_USAGE",
		Help: "Memory Usage by Dapr Sidecar",
	})
	applicationThroughputGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "APPLICATION_THROUGHPUT",
		Help: "Actual QPS",
	})

	//Create a pusher to push metrics to the Prometheus Pushgateway

	pusher := push.New("http://localhost:9091", building_block).
		Collector(daprLatencyGauge).
		Collector(sidecarCpuGauge).
		Collector(sidecarMemoryGauge).
		Collector(applicationThroughputGauge).
		Grouping("building_block", building_block)

	// Add the component Grouping only if specified

	if len(component) > 0 {
		pusher.Grouping("component", component)
	}

	// Set the dapr_metrics values to the Gauges created

	daprLatencyGauge.Set(metrics.Dapr_latency)
	sidecarCpuGauge.Set(float64(metrics.Sidecar_cpu))
	sidecarMemoryGauge.Set(metrics.Sidecar_memory)
	applicationThroughputGauge.Set(metrics.Application_throughput)

	// Push the metrics value to the Pushgateway

	if err := pusher.Push(); err != nil {
		log.Println("Failed to push metrics:", err)
	}

}