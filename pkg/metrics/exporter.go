package metrics

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	ocprom "contrib.go.opencensus.io/exporter/prometheus"
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
	// Run initializes metrics exporter
	Run(context.Context) error
	// Options returns Exporter options
	Options() *Options
}

// NewExporter creates new MetricsExporter instance.
func NewExporter(logger logger.Logger, namespace string) Exporter {
	return NewExporterWithOptions(logger, namespace, DefaultMetricOptions())
}

// NewExporterWithOptions creates new MetricsExporter instance with options.
func NewExporterWithOptions(logger logger.Logger, namespace string, options *Options) Exporter {
	// TODO: support multiple exporters
	return &promMetricsExporter{
		exporter: &exporter{
			namespace: namespace,
			options:   options,
			logger:    logger,
		},
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
	server     *http.Server
}

// Run initializes and runs the opencensus exporter.
func (m *promMetricsExporter) Run(ctx context.Context) error {
	if !m.exporter.Options().MetricsEnabled {
		// Block until context is cancelled.
		<-ctx.Done()
		return nil
	}

	var err error
	if m.ocExporter, err = ocprom.NewExporter(ocprom.Options{
		Namespace: m.namespace,
		Registry:  prom.DefaultRegisterer.(*prom.Registry),
	}); err != nil {
		return fmt.Errorf("failed to create Prometheus exporter: %w", err)
	}

	// start metrics server
	return m.startMetricServer(ctx)
}

// startMetricServer starts metrics server.
func (m *promMetricsExporter) startMetricServer(ctx context.Context) error {
	if !m.exporter.Options().MetricsEnabled {
		// skip if metrics is not enabled
		return nil
	}

	addr := fmt.Sprintf(":%d", m.options.MetricsPort())

	if m.ocExporter == nil {
		return errors.New("exporter was not initialized")
	}

	m.exporter.logger.Infof("metrics server started on %s%s", addr, defaultMetricsPath)
	mux := http.NewServeMux()
	mux.Handle(defaultMetricsPath, m.ocExporter)

	m.server = &http.Server{
		Addr:        addr,
		Handler:     mux,
		ReadTimeout: time.Second * 10,
	}

	errCh := make(chan error)

	go func() {
		if err := m.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("failed to run metrics server: %v", err)
			return
		}
		errCh <- nil
	}()

	var err error
	select {
	case <-ctx.Done():
	case err = <-errCh:
		close(errCh)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	return errors.Join(m.server.Shutdown(ctx), err, <-errCh)
}
