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

	OutboundPolicyFlowDirection PolicyFlowDirection = "outbound"
	InboundPolicyFlowDirection  PolicyFlowDirection = "inbound"
)

type PolicyType string

type PolicyFlowDirection string

type resiliencyMetrics struct {
	policiesLoadCount *stats.Int64Measure
	executionCount    *stats.Int64Measure
	activationsCount  *stats.Int64Measure

	appID   string
	ctx     context.Context
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
			"Number of times a resiliency policyKey has been applied to a building block.",
			stats.UnitDimensionless),
		activationsCount: stats.Int64(
			"resiliency/activations_total",
			"Number of times a resiliency policyKey has been activated in a building block after a failure or after a state change.",
			stats.UnitDimensionless),

		// TODO: how to use correct context
		ctx:     context.Background(),
		enabled: false,
	}
}

// Init registers the resiliency metrics views.
func (m *resiliencyMetrics) Init(id string) error {
	m.enabled = true
	m.appID = id
	return view.Register(
		diagUtils.NewMeasureView(m.policiesLoadCount, []tag.Key{appIDKey, resiliencyNameKey, namespaceKey}, view.Count()),
		diagUtils.NewMeasureView(m.executionCount, []tag.Key{appIDKey, resiliencyNameKey, policyKey, namespaceKey, flowDirectionKey, targetKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(m.activationsCount, []tag.Key{appIDKey, resiliencyNameKey, policyKey, namespaceKey, flowDirectionKey, targetKey, statusKey}, view.Count()),
	)
}

// PolicyLoaded records metric when policy is loaded.
func (m *resiliencyMetrics) PolicyLoaded(resiliencyName, namespace string) {
	if m.enabled {
		_ = stats.RecordWithTags(
			m.ctx,
			diagUtils.WithTags(m.policiesLoadCount.Name(), appIDKey, m.appID, resiliencyNameKey, resiliencyName, namespaceKey, namespace),
			m.policiesLoadCount.M(1),
		)
	}
}

// PolicyWithStatusExecuted records metric when policy is executed with added status information (e.g., circuit breaker open).
func (m *resiliencyMetrics) PolicyWithStatusExecuted(resiliencyName, namespace string, policy PolicyType, flowDirection PolicyFlowDirection, target string, status string) {
	if m.enabled {
		_ = stats.RecordWithTags(
			m.ctx,
			diagUtils.WithTags(m.executionCount.Name(), appIDKey, m.appID, resiliencyNameKey, resiliencyName, policyKey, string(policy),
				namespaceKey, namespace, flowDirectionKey, string(flowDirection), targetKey, target, statusKey, status),
			m.executionCount.M(1),
		)
	}
}

// PolicyExecuted records metric when policy is executed.
func (m *resiliencyMetrics) PolicyExecuted(resiliencyName, namespace string, policy PolicyType, flowDirection PolicyFlowDirection, target string) {
	m.PolicyWithStatusExecuted(resiliencyName, namespace, policy, flowDirection, target, "")
}

// PolicyActivated records metric when policy is activated after a failure
func (m *resiliencyMetrics) PolicyActivated(resiliencyName, namespace string, policy PolicyType, flowDirection PolicyFlowDirection, target string) {
	m.PolicyWithStatusActivated(resiliencyName, namespace, policy, flowDirection, target, "")
}

// PolicyWithStatusActivated records metric when policy is activated after a failure or in the case of circuit breaker after a state change. with added state/status (e.g., circuit breaker open).
func (m *resiliencyMetrics) PolicyWithStatusActivated(resiliencyName, namespace string, policy PolicyType, flowDirection PolicyFlowDirection, target string, status string) {
	if m.enabled {
		_ = stats.RecordWithTags(
			m.ctx,
			diagUtils.WithTags(m.activationsCount.Name(), appIDKey, m.appID, resiliencyNameKey, resiliencyName, policyKey, string(policy),
				namespaceKey, namespace, flowDirectionKey, string(flowDirection), targetKey, target, statusKey, status),
			m.activationsCount.M(1),
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
