package diagnostics

import (
	"context"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	"github.com/dapr/dapr/pkg/diagnostics/utils"
)

var (
	CircuitBreakerPolicy PolicyType = "circuitbreaker"
	RetryPolicy          PolicyType = "retry"
	TimeoutPolicy        PolicyType = "timeout"

	OutboundPolicyFlowDirection PolicyFlowDirection = "outbound"
	InboundPolicyFlowDirection  PolicyFlowDirection = "inbound"
)

type PolicyType string

type PolicyFlowDirection string

type resiliencyMetrics struct {
	policiesLoadCount *stats.Int64Measure
	executionCount    *stats.Int64Measure
	activationsCount  *stats.Int64Measure

	meter    view.Meter
	regRules utils.Rules
	appID    string
	enabled  bool
}

func newResiliencyMetrics(meter view.Meter, regRules utils.Rules) *resiliencyMetrics {
	return &resiliencyMetrics{ //nolint:exhaustruct
		meter:    meter,
		regRules: regRules,
		policiesLoadCount: stats.Int64(
			"resiliency/loaded",
			"Number of resiliency policies loaded.",
			stats.UnitDimensionless),
		executionCount: stats.Int64(
			"resiliency/count",
			"Number of times a resiliency policyKey has been applied to a building block.",
			stats.UnitDimensionless),
		activationsCount: stats.Int64(
			"resiliency/activations_total",
			"Number of times a resiliency policyKey has been activated in a building block after a failure or after a state change.",
			stats.UnitDimensionless),

		enabled: false,
	}
}

// init registers the resiliency metrics views.
func (m *resiliencyMetrics) init(id string) error {
	m.enabled = true
	m.appID = id
	return m.meter.Register(
		utils.NewMeasureView(m.policiesLoadCount, []tag.Key{appIDKey, resiliencyNameKey, namespaceKey}, utils.Count()),
		utils.NewMeasureView(m.executionCount, []tag.Key{appIDKey, resiliencyNameKey, policyKey, namespaceKey, flowDirectionKey, targetKey, statusKey}, utils.Count()),
		utils.NewMeasureView(m.activationsCount, []tag.Key{appIDKey, resiliencyNameKey, policyKey, namespaceKey, flowDirectionKey, targetKey, statusKey}, utils.Count()),
	)
}

// PolicyLoaded records metric when policy is loaded.
func (m *resiliencyMetrics) PolicyLoaded(ctx context.Context, resiliencyName, namespace string) {
	if m.enabled {
		_ = stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(m.meter),
			m.regRules.WithTags(m.policiesLoadCount.Name(), appIDKey, m.appID, resiliencyNameKey, resiliencyName, namespaceKey, namespace),
			stats.WithMeasurements(m.policiesLoadCount.M(1)),
		)
	}
}

// PolicyWithStatusExecuted records metric when policy is executed with added status information (e.g., circuit breaker open).
func (m *resiliencyMetrics) PolicyWithStatusExecuted(ctx context.Context, resiliencyName, namespace string, policy PolicyType, flowDirection PolicyFlowDirection, target string, status string) {
	if m.enabled {
		_ = stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(m.meter),
			m.regRules.WithTags(m.executionCount.Name(), appIDKey, m.appID, resiliencyNameKey, resiliencyName, policyKey, string(policy),
				namespaceKey, namespace, flowDirectionKey, string(flowDirection), targetKey, target, statusKey, status),
			stats.WithMeasurements(m.executionCount.M(1)),
		)
	}
}

// PolicyExecuted records metric when policy is executed.
func (m *resiliencyMetrics) PolicyExecuted(ctx context.Context, resiliencyName, namespace string, policy PolicyType, flowDirection PolicyFlowDirection, target string) {
	m.PolicyWithStatusExecuted(ctx, resiliencyName, namespace, policy, flowDirection, target, "")
}

// PolicyActivated records metric when policy is activated after a failure
func (m *resiliencyMetrics) PolicyActivated(ctx context.Context, resiliencyName, namespace string, policy PolicyType, flowDirection PolicyFlowDirection, target string) {
	m.PolicyWithStatusActivated(ctx, resiliencyName, namespace, policy, flowDirection, target, "")
}

// PolicyWithStatusActivated records metric when policy is activated after a failure or in the case of circuit breaker after a state change. with added state/status (e.g., circuit breaker open).
func (m *resiliencyMetrics) PolicyWithStatusActivated(ctx context.Context, resiliencyName, namespace string, policy PolicyType, flowDirection PolicyFlowDirection, target string, status string) {
	if m.enabled {
		_ = stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(m.meter),
			m.regRules.WithTags(m.activationsCount.Name(), appIDKey, m.appID, resiliencyNameKey, resiliencyName, policyKey, string(policy),
				namespaceKey, namespace, flowDirectionKey, string(flowDirection), targetKey, target, statusKey, status),
			stats.WithMeasurements(m.activationsCount.M(1)),
		)
	}
}

func ResiliencyActorTarget(actorType string) string {
	return "actor_" + actorType
}

func ResiliencyAppTarget(app string) string {
	return "app_" + app
}

func ResiliencyComponentTarget(name string, componentType string) string {
	return componentType + "_" + name
}
