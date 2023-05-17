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
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/diagnostics/utils"
)

var (
	// appIDKey is a tag key for App ID.
	appIDKey = tag.MustNewKey("app_id")

	// DefaultReportingPeriod is the default view reporting period.
	DefaultReportingPeriod = 1 * time.Minute
)

// Metrics is a collection of metrics.
type Metrics struct {
	Meter view.Meter

	// Service holds service monitoring metrics definitions.
	Service *serviceMetrics

	// GRPC holds default gRPC monitoring handlers and middlewares.
	GRPC *grpcMetrics

	// HTTP holds default HTTP monitoring handlers and middlewares.
	HTTP *httpMetrics

	// Component holds component specific metrics.
	Component *componentMetrics

	// Resiliency holds resiliency specific metrics.
	Resiliency *resiliencyMetrics

	// Rules holds regex expressions for metrics labels
	Rules utils.Rules
}

// NewMetrics initializes metrics.
func NewMetrics(rules []config.MetricsRule) (*Metrics, error) {
	meter := view.NewMeter()
	meter.Start()

	// Set reporting period of views
	meter.SetReportingPeriod(DefaultReportingPeriod)

	clock := clock.RealClock{}

	regRules, err := utils.CreateRulesMap(rules)
	if err != nil {
		return nil, err
	}

	return &Metrics{
		Meter:      meter,
		Service:    newServiceMetrics(meter, clock, regRules),
		GRPC:       newGRPCMetrics(meter, clock, regRules),
		HTTP:       newHTTPMetrics(meter, clock, regRules),
		Component:  newComponentMetrics(meter, regRules),
		Resiliency: newResiliencyMetrics(meter, regRules),
		Rules:      regRules,
	}, nil
}

func (m *Metrics) Init(appID, namespace string) error {
	if err := m.Service.init(appID); err != nil {
		return err
	}

	if err := m.GRPC.init(appID); err != nil {
		return err
	}

	if err := m.HTTP.init(appID); err != nil {
		return err
	}

	if err := m.Component.init(appID, namespace); err != nil {
		return err
	}

	if err := m.Resiliency.init(appID); err != nil {
		return err
	}

	return nil
}

// Stop stops the metrics collection.
func (m *Metrics) Stop() {
	m.Meter.Stop()
}
