package metrics

import (
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/push"

	ocprom "contrib.go.opencensus.io/exporter/prometheus"
	"github.com/dapr/dapr/pkg/logger"
	"github.com/pkg/errors"
	prom "github.com/prometheus/client_golang/prometheus"
	"go.opencensus.io/stats/view"
)

const (
	// DefaultMetricNamespace is the prefix of metric name
	DefaultMetricNamespace = "dapr"
	defaultMetricsPath     = "/"
)

// Exporter is the interface for metrics exporters
type Exporter interface {
	// Init initializes metrics exporter
	Init() error
	// Options returns Exporter options
	Options() *Options
}

// NewExporter creates new MetricsExporter instance
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

// exporter is the base struct
type exporter struct {
	namespace string
	options   *Options
	logger    logger.Logger
}

// Options returns current metric exporter options
func (m *exporter) Options() *Options {
	return m.options
}

// promMetricsExporter is prometheus metric exporter
type promMetricsExporter struct {
	*exporter
	ocExporter *ocprom.Exporter
}

// Init initializes opencensus exporter
func (m *promMetricsExporter) Init() error {
	if !m.exporter.Options().MetricsEnabled {
		return nil
	}

	// Add default health metrics for process
	registry := prom.NewRegistry()
	registry.MustRegister(prom.NewProcessCollector(prom.ProcessCollectorOpts{}))
	registry.MustRegister(prom.NewGoCollector())

	var err error
	m.ocExporter, err = ocprom.NewExporter(ocprom.Options{
		Namespace: m.namespace,
		Registry:  registry,
	})

	if err != nil {
		return errors.Errorf("failed to create Prometheus exporter: %v", err)
	}

	// register exporter to view
	view.RegisterExporter(m.ocExporter)

	// start metrics server
	err = m.startMetricServer()
	if err != nil {
		return err
	}

	opts := m.exporter.Options()
	// check if start pushgatway support
	if !opts.pushGatewayEnabled {
		return nil
	}

	// check if pushgateway opts are valid
	if err := opts.CheckParam(); err != nil {
		return errors.Errorf("pushgateway options invalid: %v", err)
	}

	go func() {
		pusher := push.New("http://"+opts.pushGatewayHost+":"+opts.pushGatewayPort, m.namespace).Gatherer(registry)
		if e := recover(); e != nil {
			m.exporter.logger.Errorf("failed to push to pushgateway server: %v", e)
		}
		for {
			if err := pusher.Push(); err != nil {
				m.exporter.logger.Errorf("failed to push to pushgateway server: %v", err)
			}
			time.Sleep(time.Second * time.Duration(opts.pushGatewayUpdateInterval))
		}
	}()
	return nil
}

// startMetricServer starts metrics server
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
