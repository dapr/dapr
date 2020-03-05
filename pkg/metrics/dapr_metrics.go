package metrics

import (
	"errors"
	"fmt"
	"net/http"

	"contrib.go.opencensus.io/exporter/prometheus"
	"github.com/dapr/dapr/pkg/logger"
)

const (
	// DefaultMetricNamespace is the prefix of metric name
	DefaultMetricNamespace = "dapr"
	defaultMetricsPath     = "/"
)

// DaprMetricExporter is the metric exporter for dapr
type DaprMetricExporter struct {
	exporter  *prometheus.Exporter
	options   *Options
	namespace string
	logger    logger.Logger
}

// NewDaprMetricExporter creates new DaprMetricExporter instance
func NewDaprMetricExporter() DaprMetricExporter {
	return DaprMetricExporter{
		options: defaultMetricOptions(),
		logger:  logger.NewLogger("dapr.metrics"),
	}
}

// Options returns current metric exporter options
func (m *DaprMetricExporter) Options() *Options {
	return m.options
}

// Init initializes opencensus exporter
func (m *DaprMetricExporter) Init(namespace string) {
	m.namespace = namespace

	if !m.Options().MetricsEnabled {
		return
	}

	var err error
	// TODO: support multiple exporters
	m.exporter, err = prometheus.NewExporter(prometheus.Options{
		Namespace: namespace,
	})

	if err != nil {
		m.logger.Fatalf("Failed to create Prometheus exporter: %v", err)
	}
}

// StartMetricServer starts metrics server
func (m *DaprMetricExporter) StartMetricServer() error {
	if !m.Options().MetricsEnabled {
		// skip if metrics is not enabled
		return nil
	}

	addr := fmt.Sprintf(":%d", m.options.MetricsPort())

	if m.exporter == nil {
		return errors.New("exporter was not initiailized")
	}

	m.logger.Infof("metrics server started on %s%s", addr, defaultMetricsPath)
	go func() {
		mux := http.NewServeMux()
		mux.Handle(defaultMetricsPath, m.exporter)

		if err := http.ListenAndServe(addr, mux); err != nil {
			m.logger.Fatalf("Failed to start metrics server: %v", err)
		}
	}()

	return nil
}
