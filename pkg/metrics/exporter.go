package metrics

import (
	"fmt"
	"net/http"

	ocprom "contrib.go.opencensus.io/exporter/prometheus"
	"github.com/pkg/errors"
	prom "github.com/prometheus/client_golang/prometheus"

	"github.com/dapr/kit/logger"
)

const (
	// DefaultMetricNamespace is the prefix of metric name.
	DefaultMetricNamespace = "dapr"
	defaultMetricsPath     = "/"
)

// Exporter is the interface for metrics exporters.
type Exporter interface {
	// Init initializes metrics exporter
	Init() error
	// Options returns Exporter options
	Options() *Options
}

// NewExporter creates new MetricsExporter instance.
func NewExporter(namespace string) Exporter {
	// TODO: support multiple exporters
	return &promMetricsExporter{
		&exporter{
			namespace: namespace,
			options:   defaultMetricOptions(),
			logger:    logger.NewLogger("dapr.metrics"),
		},
		nil,
	}
}

// exporter is the base struct.
type exporter struct {
	namespace string
	options   *Options
	logger    logger.Logger
}

// Options returns current metric exporter options.
func (m *exporter) Options() *Options {
	return m.options
}

// promMetricsExporter is prometheus metric exporter.
type promMetricsExporter struct {
	*exporter
	ocExporter *ocprom.Exporter
}

// Init initializes opencensus exporter.
func (m *promMetricsExporter) Init() error {
	if !m.exporter.Options().MetricsEnabled {
		return nil
	}

	var err error
	if m.ocExporter, err = ocprom.NewExporter(ocprom.Options{
		Namespace: m.namespace,
		Registry:  prom.DefaultRegisterer.(*prom.Registry),
	}); err != nil {
		return errors.Errorf("failed to create Prometheus exporter: %v", err)
	}

	// start metrics server
	return m.startMetricServer()
}

// startMetricServer starts metrics server.
func (m *promMetricsExporter) startMetricServer() error {
	if !m.exporter.Options().MetricsEnabled {
		// skip if metrics is not enabled
		return nil
	}

	addr := fmt.Sprintf(":%d", m.options.MetricsPort())

	if m.ocExporter == nil {
		return errors.New("exporter was not initialized")
	}

	m.exporter.logger.Infof("metrics server started on %s%s", addr, defaultMetricsPath)
	go func() {
		mux := http.NewServeMux()
		mux.Handle(defaultMetricsPath, m.ocExporter)

		if err := http.ListenAndServe(addr, mux); err != nil {
			m.exporter.logger.Fatalf("failed to start metrics server: %v", err)
		}
	}()

	return nil
}
