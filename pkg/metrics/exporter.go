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

package metrics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	ocprom "contrib.go.opencensus.io/exporter/prometheus"
	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/kit/logger"
)

const (
	// DefaultMetricNamespace is the prefix of metric name.
	DefaultMetricNamespace = "dapr"
	defaultMetricsPath     = "/"
)

// Exporter is the interface for metrics exporters.
type Exporter interface {
	// Start initializes metrics exporter.
	Start(context.Context) error
}

// exporter is the base struct.
type exporter struct {
	namespace     string
	enabled       bool
	port          string
	listenAddress string
	logger        logger.Logger
	htarget       healthz.Target
	otlpOpts      *OTLPOptions
	otlpBridge    *otlpBridge
	appID         string
}

// New creates new metrics Exporter instance with given options.
func New(opts Options) Exporter {
	return &exporter{
		htarget:       opts.Healthz.AddTarget("metrics-exporter"),
		namespace:     opts.Namespace,
		logger:        opts.Log,
		enabled:       opts.Enabled,
		port:          opts.Port,
		listenAddress: opts.ListenAddress,
		appID:         opts.AppID,
		otlpOpts:      opts.OTLP,
	}
}

// Start initializes and runs the opencensus exporter (Prometheus endpoint)
// and, if configured, the OTLP metrics bridge.
func (e *exporter) Start(ctx context.Context) error {
	if !e.enabled {
		e.htarget.Ready()
		// Block until context is cancelled.
		<-ctx.Done()
		return nil
	}

	// Initialize the OTLP bridge if configured.
	// The bridge is created during Start() so it runs in the exporter's
	// lifecycle context. OTLP options are set at construction time via Options.OTLP.
	if e.otlpOpts != nil && e.appID != "" {
		bridge, bridgeErr := newOTLPBridge(ctx, *e.otlpOpts, e.appID)
		if bridgeErr != nil {
			e.logger.Warnf("Failed to initialize OTLP metrics bridge: %v", bridgeErr)
		} else {
			e.otlpBridge = bridge
			e.logger.Info("OTLP metrics bridge started")
		}
	}

	port, err := strconv.Atoi(e.port)
	if err != nil {
		return fmt.Errorf("failed to parse metrics port: %w", err)
	}

	reg := prom.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	ocExporter, err := ocprom.NewExporter(ocprom.Options{
		Namespace: e.namespace,
		Registry:  reg,
	})
	if err != nil {
		return fmt.Errorf("failed to create Prometheus exporter: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", e.listenAddress, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	e.logger.Infof("metrics server started on %s%s", addr, defaultMetricsPath)
	mux := http.NewServeMux()
	mux.Handle(defaultMetricsPath, ocExporter)

	server := &http.Server{
		Handler:     mux,
		ReadTimeout: time.Second * 10,
	}

	errCh := make(chan error)

	go func() {
		if serr := server.Serve(ln); serr != nil && !errors.Is(serr, http.ErrServerClosed) {
			errCh <- fmt.Errorf("failed to run metrics server: %v", serr)
			return
		}
		errCh <- nil
	}()

	e.htarget.Ready()

	select {
	case <-ctx.Done():
	case err = <-errCh:
		close(errCh)
	}

	// Shut down the OTLP metrics bridge before the Prometheus server.
	// This ensures pending metrics are flushed.
	if e.otlpBridge != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if shutdownErr := e.otlpBridge.Shutdown(shutdownCtx); shutdownErr != nil {
			e.logger.Errorf("Error shutting down OTLP metrics bridge: %v", shutdownErr)
		}
		shutdownCancel()
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	return errors.Join(server.Shutdown(ctx), err, <-errCh)
}
