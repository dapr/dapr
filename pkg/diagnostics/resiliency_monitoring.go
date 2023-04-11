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

	AppPolicyTarget       PolicyTarget = "app"
	ComponentPolicyTarget PolicyTarget = "component"
	PubSubPolicyTarget    PolicyTarget = "pubsub"
	ActorsPolicyTarget    PolicyTarget = "actors"

	OutboundPolicyFlowDirection PolicyFlowDirection = "outbound"
	InboundPolicyFlowDirection  PolicyFlowDirection = "inbound"
)

type PolicyType string

type PolicyTarget string

type PolicyFlowDirection string

type resiliencyMetrics struct {
	policiesLoadCount *stats.Int64Measure
	executionCount    *stats.Int64Measure

	appID                 string
	ctx                   context.Context
	enabled               bool
	defaultMetricsEnabled bool
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

		// TODO: how to use correct context
		ctx:     context.Background(),
		enabled: false,
	}
}

// Init registers the resiliency metrics views.
func (m *resiliencyMetrics) Init(id string, defaultMetricsEnabled bool) error {
	m.enabled = true
	m.appID = id
	m.defaultMetricsEnabled = defaultMetricsEnabled
	return view.Register(
		diagUtils.NewMeasureView(m.policiesLoadCount, []tag.Key{appIDKey, resiliencyNameKey, namespaceKey}, view.Count()),
		diagUtils.NewMeasureView(m.executionCount, []tag.Key{appIDKey, resiliencyNameKey, policyKey, namespaceKey, flowDirectionKey, targetKey, statusKey}, view.Count()),
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

// PolicyWithStatusExecuted records metric when policy is executed.
func (m *resiliencyMetrics) PolicyWithStatusExecuted(resiliencyName, namespace string, policy PolicyType, flowDirection PolicyFlowDirection, target PolicyTarget, status string) {
	if m.enabled {
		_ = stats.RecordWithTags(
			m.ctx,
			diagUtils.WithTags(m.executionCount.Name(), appIDKey, m.appID, resiliencyNameKey, resiliencyName, policyKey, string(policy), namespaceKey, namespace, flowDirectionKey, string(flowDirection), targetKey, string(target), statusKey, status),
			m.executionCount.M(1),
		)
	}
}

// PolicyExecuted records metric when policy is executed.
func (m *resiliencyMetrics) PolicyExecuted(resiliencyName, namespace string, policy PolicyType, flowDirection PolicyFlowDirection, target PolicyTarget) {
	m.PolicyWithStatusExecuted(resiliencyName, namespace, policy, flowDirection, target, "")
}

// DefaultPolicyExecuted records metric when policy is executed.
func (m *resiliencyMetrics) DefaultPolicyExecuted(resiliencyName, namespace string, policy PolicyType, flowDirection PolicyFlowDirection, target PolicyTarget) {
	if m.defaultMetricsEnabled {
		m.PolicyWithStatusExecuted(resiliencyName, namespace, policy, flowDirection, target, "")
	}
}

// DefaultPolicyWithStatusExecuted records metric when policy is executed.
func (m *resiliencyMetrics) DefaultPolicyWithStatusExecuted(resiliencyName, namespace string, policy PolicyType, flowDirection PolicyFlowDirection, target PolicyTarget, status string) {
	if m.defaultMetricsEnabled {
		m.PolicyWithStatusExecuted(resiliencyName, namespace, policy, flowDirection, target, status)
	}
}
