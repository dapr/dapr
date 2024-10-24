package metrics

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"contrib.go.opencensus.io/exporter/ocagent"
	ocprom "contrib.go.opencensus.io/exporter/prometheus"
	prom "github.com/prometheus/client_golang/prometheus"
	"go.opencensus.io/stats/view"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/kit/logger"

	"google.golang.org/grpc/credentials"
)

const (
	// DefaultMetricNamespace is the prefix of metric name.
	DefaultMetricNamespace = "dapr"
	defaultMetricsPath     = "/"
)

// Exporter is the interface for metrics exporters.
type Exporter interface {
	// Start initializes metrics exporter
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
	push          PushToCollectorOptions
}

// New creates new metrics Exporter instance with given options.
func New(opts Options) Exporter {
	return &exporter{
		htarget:       opts.Healthz.AddTarget(),
		namespace:     opts.Namespace,
		logger:        opts.Log,
		enabled:       opts.Enabled,
		port:          opts.Port,
		listenAddress: opts.ListenAddress,
		push:          opts.PushToCollector,
	}
}

// Start initializes and runs the opencensus exporter.
func (e *exporter) Start(ctx context.Context) error {
	if !e.enabled {
		e.htarget.Ready()
		// Block until context is cancelled.
		<-ctx.Done()
		return nil
	}

	port, err := strconv.Atoi(e.port)
	if err != nil {
		return fmt.Errorf("failed to parse metrics port: %w", err)
	}
	ocExporter, err := ocprom.NewExporter(ocprom.Options{
		Namespace: e.namespace,
		Registry:  prom.DefaultRegisterer.(*prom.Registry),
	})
	if err != nil {
		return fmt.Errorf("failed to create Prometheus exporter: %w", err)
	}
	if er := e.setupPushToCollector(); er != nil {
		return fmt.Errorf("failed to create tls settings for metrics push: %w", err)
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

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	return errors.Join(server.Shutdown(ctx), err, <-errCh)
}

func (e *exporter) setupPushToCollector() error {
	if e.push.Enabled {
		opts := []ocagent.ExporterOption{
			ocagent.WithReconnectionPeriod(5 * time.Second),
			ocagent.WithAddress(e.push.Endpoint),
		}
		if len(e.push.Headers) > 0 {
			opts = append(opts, ocagent.WithHeaders(e.push.Headers))
		}
		secure := "insecure"
		if len(e.push.TLS.CaFile) > 0 && len(e.push.TLS.KeyFile) > 0 && len(e.push.TLS.CertFile) > 0 {
			caCert, err := e.loadCaCert(e.push.TLS.CaFile)
			if err != nil {
				return err
			}
			cert, err := e.loadCertificate(e.push.TLS.KeyFile, e.push.TLS.CertFile)
			if err != nil {
				return err
			}
			opts = append(opts, ocagent.WithTLSCredentials(credentials.NewTLS(&tls.Config{
				RootCAs:      caCert,
				Certificates: []tls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
			})))
			secure = "TLS"
		} else {
			opts = append(opts, ocagent.WithInsecure())
		}
		ocagentExporter, err := ocagent.NewExporter(opts...)
		if err != nil {
			return fmt.Errorf("failed to create oc agent exporter: %w", err)
		}
		view.RegisterExporter(ocagentExporter)
		view.SetReportingPeriod(20 * time.Second)
		e.logger.Infof("Pushing metrics to %s using %s connection", e.push.Endpoint, secure)
	}
	return nil
}

func (e *exporter) loadCaCert(caPath string) (*x509.CertPool, error) {
	caPEM, err := os.ReadFile(filepath.Clean(caPath))
	if err != nil {
		return nil, fmt.Errorf("failed to load CA %s: %w", caPath, err)
	}
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("failed to parse CA %s", caPath)
	}
	return certPool, nil
}

func (e *exporter) loadCertificate(keyPath string, certPath string) (tls.Certificate, error) {
	var certPem, keyPem []byte
	var err error
	certPem, err = os.ReadFile(certPath)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPem, err = os.ReadFile(keyPath)
	if err != nil {
		return tls.Certificate{}, err
	}
	certificate, err := tls.X509KeyPair(certPem, keyPem)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to load TLS cert and key PEMs: %w", err)
	}
	return certificate, err
}
