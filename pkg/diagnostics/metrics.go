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
	"github.com/dapr/kit/logger"
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

var (
	// <<10 -> KBs; <<20 -> MBs; <<30 -> GBs
	defaultSizeDistribution = view.Distribution(1<<10, 2<<10, 4<<10, 16<<10, 64<<10, 256<<10, 1<<20, 4<<20, 16<<20, 64<<20, 256<<20, 1<<30, 4<<30)
	latencyDistribution     *view.Aggregation
)

// SetLatencyDistribution sets the latencyDistribution variable to the proper aggregation
//
// This function is primarily required because in various places, exclusively
// for testing from my review, we call the x.Init() functions that InitMetrics
// would typically call in actual runtime and correctly set the global metrics
// with proper aggregations.  When defaultLatencyDistribution was a static
// variable this was not a problem because it was a constant pointer to the same
// instance.  When we want to dynamically set latencyDistribution based on
// potential user input this becomes more complicated.
//
// The views that are getting registered by the functions that consume the
// latencyDistribution are global.  If we try to use a variable that calls a
// function to retrieve the *view.Aggregation, even though the buckets and names
// may be exactly the same, it is a distinct *view.Aggregation and OpenCensus
// treats it as such and errors out.
func SetLatencyDistribution(aggregation *view.Aggregation) error {
	log := logger.NewLogger("metrics")
	// If it's already been set by something else we won't change it.  I have
	// primarily encountered this case in unit testing.
	if latencyDistribution != nil {
		return nil
	}
	// If we're passing in an explicit aggregation, use that.  This is the happy
	// path for InitMetrics in a typical startup sequence.
	//
	// Otherwise, use default, e.g. pkg/api/grpc/proxy/handler_test.go.
	if aggregation != nil {
		latencyDistribution = aggregation
	} else {
		latencyDistribution = config.LoadDefaultConfiguration().GetMetricsSpec().GetLatencyDistribution(log)
	}
	return nil
}

// InitMetrics initializes metrics.
func InitMetrics(appID, namespace string, metricSpec config.MetricSpec) error {
	log := logger.NewLogger("diagnostics")
	err := SetLatencyDistribution(metricSpec.GetLatencyDistribution(log))
	if err != nil {
		return err
	}

	if err := DefaultMonitoring.Init(appID); err != nil {
		return err
	}

	if err := DefaultGRPCMonitoring.Init(appID); err != nil {
		return err
	}

	if err := DefaultHTTPMonitoring.Init(appID, metricSpec.GetEnabled()); err != nil {
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
	return utils.CreateRulesMap(metricSpec.Rules)
}
