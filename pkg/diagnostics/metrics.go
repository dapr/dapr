// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package diagnostics

import (
	"time"

	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

var (
	// appIDKey is a tag key for App ID
	appIDKey = tag.MustNewKey("app_id")
)

var (
	// DefaultReportingPeriod is the default view reporting period
	DefaultReportingPeriod = 1 * time.Minute

	// DefaultMonitoring holds service monitoring metrics definitions
	DefaultMonitoring = newServiceMetrics()
	// DefaultGRPCMonitoring holds default gRPC monitoring handlers and middlewares
	DefaultGRPCMonitoring = newGRPCMetrics()
	// DefaultHTTPMonitoring holds default HTTP monitoring handlers and middlewares
	DefaultHTTPMonitoring = newHTTPMetrics()
)

// InitMetrics initializes metrics
func InitMetrics(appID string) error {
	if err := DefaultMonitoring.Init(appID); err != nil {
		return err
	}

	if err := DefaultGRPCMonitoring.Init(appID); err != nil {
		return err
	}

	if err := DefaultHTTPMonitoring.Init(appID); err != nil {
		return err
	}

	// Set reporting period of views
	view.SetReportingPeriod(DefaultReportingPeriod)

	return nil
}
