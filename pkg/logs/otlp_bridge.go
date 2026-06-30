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

package logs

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/bridges/otellogrus"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	daprotel "github.com/dapr/dapr/pkg/otel"
	"github.com/dapr/kit/logger"
)

const defaultOTLPLogTimeout = 15 * time.Second

// OTLPOptions contains options for the OTLP log exporter.
type OTLPOptions struct {
	Protocol       string
	EndpointAddress string
	IsSecure       bool
	Headers        map[string]string
	Timeout        time.Duration
}

// Bridge manages the logrus-to-OTLP log bridge.
// It installs a logrus Hook that forwards all log entries
// to an OTLP endpoint via the OpenTelemetry Logs SDK.
type Bridge struct {
	logger         logger.Logger
	loggerProvider *sdklog.LoggerProvider
	hook           *otellogrus.Hook
}

// NewBridge creates a new OTLP log bridge.
// It sets up an OTLP exporter, LoggerProvider, and creates
// an otellogrus Hook for installation on logrus loggers.
func NewBridge(ctx context.Context, opts OTLPOptions, appID string) (*Bridge, error) {
	log := logger.NewLogger("dapr.logs.otlp")

	exporter, err := daprotel.NewLogExporter(ctx, daprotel.ConnConfig{
		Protocol:       opts.Protocol,
		EndpointAddress: opts.EndpointAddress,
		IsSecure:       opts.IsSecure,
		Headers:        opts.Headers,
		Timeout:        opts.Timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP log exporter: %w", err)
	}

	res, resErr := daprotel.NewResource(ctx, appID)
	if resErr != nil {
		log.Warnf("failed to create OTLP log resource from env, using default: %v", resErr)
	}

	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
		sdklog.WithResource(res),
	)

	hook := otellogrus.NewHook("dapr",
		otellogrus.WithLoggerProvider(provider),
	)

	log.Infof(
		"OTLP log bridge initialized: endpoint=%s protocol=%s",
		opts.EndpointAddress, opts.Protocol,
	)

	return &Bridge{
		logger:         log,
		loggerProvider: provider,
		hook:           hook,
	}, nil
}

// Hook returns the logrus.Hook to be added to logrus loggers.
func (b *Bridge) Hook() logrus.Hook {
	return b.hook
}

// Shutdown gracefully shuts down the OTLP log bridge, flushing pending logs.
func (b *Bridge) Shutdown(ctx context.Context) error {
	b.logger.Info("Shutting down OTLP log bridge")
	if err := b.loggerProvider.ForceFlush(ctx); err != nil {
		b.logger.Warnf("Error flushing OTLP logs: %v", err)
	}
	return b.loggerProvider.Shutdown(ctx)
}
