/*
Copyright 2021 The Dapr Authors
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

package diagnostics

import (
	"time"

	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/diagnostics/utils"
)

// appIDKey is a tag key for App ID.
var appIDKey = tag.MustNewKey("app_id")

var (
	// DefaultReportingPeriod is the default view reporting period.
	DefaultReportingPeriod = 1 * time.Minute

	// DefaultMonitoring holds service monitoring metrics definitions.
	DefaultMonitoring = newServiceMetrics()
	// DefaultGRPCMonitoring holds default gRPC monitoring handlers and middlewares.
	DefaultGRPCMonitoring = newGRPCMetrics()
	// DefaultHTTPMonitoring holds default HTTP monitoring handlers and middlewares.
	DefaultHTTPMonitoring = newHTTPMetrics()
	// DefaultComponentMonitoring holds component specific metrics.
	DefaultComponentMonitoring = newComponentMetrics()
	// DefaultResiliencyMonitoring holds resiliency specific metrics.
	DefaultResiliencyMonitoring = newResiliencyMetrics()
	// DefaultWorkflowMonitoring holds workflow specific metrics.
	DefaultWorkflowMonitoring = newWorkflowMetrics()
)

// InitMetrics initializes metrics.
func InitMetrics(appID, namespace string, rules []config.MetricsRule, legacyMetricsHTTPMetrics bool) error {
	if err := DefaultMonitoring.Init(appID); err != nil {
		return err
	}

	if err := DefaultGRPCMonitoring.Init(appID); err != nil {
		return err
	}

	if err := DefaultHTTPMonitoring.Init(appID, legacyMetricsHTTPMetrics); err != nil {
		return err
	}

	if err := DefaultComponentMonitoring.Init(appID, namespace); err != nil {
		return err
	}

	if err := DefaultResiliencyMonitoring.Init(appID); err != nil {
		return err
	}

	if err := DefaultWorkflowMonitoring.Init(appID, namespace); err != nil {
		return err
	}

	// Set reporting period of views
	view.SetReportingPeriod(DefaultReportingPeriod)
	return utils.CreateRulesMap(rules)
}
