package diagnostics

import (
	"context"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
)

var (
	CircuitBreakerPolicy PolicyType = "circuitbreaker"
	RetryPolicy          PolicyType = "retry"
	TimeoutPolicy        PolicyType = "timeout"
)

type PolicyType string

type resiliencyMetrics struct {
	policiesLoadCount *stats.Int64Measure
	executionCount    *stats.Int64Measure

	appID   string
	enabled bool
}

func newResiliencyMetrics() *resiliencyMetrics {
	return &resiliencyMetrics{ //nolint:exhaustruct
		policiesLoadCount: stats.Int64(
			"resiliency/loaded",
			"Number of resiliency policies loaded.",
			stats.UnitDimensionless),
		executionCount: stats.Int64(
			"resiliency/count",
			"Number of times a resiliency policyKey has been executed.",
			stats.UnitDimensionless),

		enabled: false,
	}
}

// Init registers the resiliency metrics views.
func (m *resiliencyMetrics) Init(id string) error {
	m.enabled = true
	m.appID = id
	return view.Register(
		diagUtils.NewMeasureView(m.policiesLoadCount, []tag.Key{appIDKey, resiliencyNameKey, namespaceKey}, view.Count()),
		diagUtils.NewMeasureView(m.executionCount, []tag.Key{appIDKey, resiliencyNameKey, policyKey, namespaceKey}, view.Count()),
	)
}

// PolicyLoaded records metric when policy is loaded.
func (m *resiliencyMetrics) PolicyLoaded(ctx context.Context, resiliencyName, namespace string) {
	if m.enabled {
		_ = stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(m.policiesLoadCount.Name(), appIDKey, m.appID, resiliencyNameKey, resiliencyName, namespaceKey, namespace),
			m.policiesLoadCount.M(1),
		)
	}
}

// PolicyExecuted records metric when policy is executed.
func (m *resiliencyMetrics) PolicyExecuted(ctx context.Context, resiliencyName, namespace string, policy PolicyType) {
	if m.enabled {
		_ = stats.RecordWithTags(
			ctx,
			diagUtils.WithTags(m.executionCount.Name(), appIDKey, m.appID, resiliencyNameKey, resiliencyName, policyKey, string(policy), namespaceKey, namespace),
			m.executionCount.M(1),
		)
	}
}
