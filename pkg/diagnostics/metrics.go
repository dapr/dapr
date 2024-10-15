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
	"github.com/dapr/dapr/pkg/messages/errorcodes"
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
	// DefaultErrorCodeMonitoring holds error code specific metrics.
	DefaultErrorCodeMonitoring = newErrorCodeMetrics()
)

// <<10 -> KBs; <<20 -> MBs; <<30 -> GBs
var defaultSizeDistribution = view.Distribution(1<<10, 2<<10, 4<<10, 16<<10, 64<<10, 256<<10, 1<<20, 4<<20, 16<<20, 64<<20, 256<<20, 1<<30, 4<<30)

// InitMetrics initializes metrics.
func InitMetrics(appID, namespace string, metricSpec config.MetricSpec) error {
	latencyDistribution := metricSpec.GetLatencyDistribution(log)
	if err := DefaultMonitoring.Init(appID, latencyDistribution); err != nil {
		return err
	}

	if err := DefaultGRPCMonitoring.Init(appID, latencyDistribution); err != nil {
		return err
	}

	httpConfig := NewHTTPMonitoringConfig(
		metricSpec.GetHTTPPathMatching(),
		metricSpec.GetHTTPIncreasedCardinality(log),
		metricSpec.GetHTTPExcludeVerbs(),
	)
	if err := DefaultHTTPMonitoring.Init(appID, httpConfig, latencyDistribution); err != nil {
		return err
	}

	if err := DefaultComponentMonitoring.Init(appID, namespace, latencyDistribution); err != nil {
		return err
	}

	if err := DefaultResiliencyMonitoring.Init(appID); err != nil {
		return err
	}

	if err := DefaultWorkflowMonitoring.Init(appID, namespace, latencyDistribution); err != nil {
		return err
	}

	log.Info("jake::: my build!!!!!!!!!!!!!")
	if metricSpec.GetRecordErrorCodes() {
		if err := DefaultErrorCodeMonitoring.Init(appID); err != nil {
			return err
		}
		log.Info("jake::: error code monitoring success")
		DefaultErrorCodeMonitoring.RecordErrorCode(errorcodes.ActorInstanceMissing)
		DefaultErrorCodeMonitoring.RecordErrorCode(errorcodes.ActorInstanceMissing)
		DefaultErrorCodeMonitoring.RecordErrorCode(errorcodes.PubsubEmpty)
	}

	// Set reporting period of views
	view.SetReportingPeriod(DefaultReportingPeriod)
	return utils.CreateRulesMap(metricSpec.Rules)
}
