package etcdhttp

// file not copied from etcd, moved vars here tho

import "github.com/prometheus/client_golang/prometheus"

var (
	healthSuccess = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "etcd",
		Subsystem: "server",
		Name:      "health_success",
		Help:      "The total number of successful health checks",
	})
	healthFailed = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "etcd",
		Subsystem: "server",
		Name:      "health_failures",
		Help:      "The total number of failed health checks",
	})
	healthCheckGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "etcd",
		Subsystem: "server",
		Name:      "healthcheck",
		Help:      "The result of each kind of healthcheck.",
	},
		[]string{"type", "name"},
	)
	healthCheckCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "etcd",
		Subsystem: "server",
		Name:      "healthchecks_total",
		Help:      "The total number of each kind of healthcheck.",
	},
		[]string{"type", "name", "status"},
	)
)
