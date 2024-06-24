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
	"sync"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/kit/logger"
	"go.opencensus.io/stats/view"
)

var (
	// <<10 -> KBs; <<20 -> MBs; <<30 -> GBs
	defaultSizeDistribution      = view.Distribution(1<<10, 2<<10, 4<<10, 16<<10, 64<<10, 256<<10, 1<<20, 4<<20, 16<<20, 64<<20, 256<<20, 1<<30, 4<<30)
	latencyDistribution          *view.Aggregation
	setupLatencyDistributionOnce sync.Once
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
	// If it's already been set by something else we won't change it.  I have
	// primarily encountered this case in unit testing.
	if latencyDistribution != nil {
		return nil
	}
	log := logger.NewLogger("latencyDistributionOnce")
	setupLatencyDistributionOnce.Do(func() {
		// If we're passing in an explicit aggregation, use that.  This is the happy
		// path for InitMetrics in a typical startup sequence.
		//
		// Otherwise, use default, e.g. pkg/api/grpc/proxy/handler_test.go.
		if aggregation != nil {
			latencyDistribution = aggregation
		} else {
			latencyDistribution = config.LoadDefaultConfiguration().GetMetricsSpec().GetLatencyDistribution(log)
		}
	})
	return nil
}

func InitGlobals(metricSpec config.MetricSpec) error {
	log := logger.NewLogger("global")
	if err := SetLatencyDistribution(metricSpec.GetLatencyDistribution(log)); err != nil {
		return err
	}
	return nil
}
