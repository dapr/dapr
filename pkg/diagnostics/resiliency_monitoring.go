package diagnostics

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
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
	meter metric.Meter

	policiesLoadCount metric.Int64Counter
	executionCount    metric.Int64Counter
	activationsCount  metric.Int64Counter

	appID   string
	ctx     context.Context
	enabled bool
}

func newResiliencyMetrics() *resiliencyMetrics {
	m := otel.Meter("resiliency")

	return &resiliencyMetrics{ //nolint:exhaustruct
		meter: m,

		// TODO: how to use correct context
		ctx:     context.Background(),
		enabled: false,
	}
}

// Init registers the resiliency metrics views.
func (m *resiliencyMetrics) Init(id string) error {
	policiesLoadCount, err := m.meter.Int64Counter(
		"resiliency.loaded",
		metric.WithDescription("Number of resiliency policies loaded."),
	)
	if err != nil {
		return err
	}

	executionCount, err := m.meter.Int64Counter(
		"resiliency.count",
		metric.WithDescription("Number of times a resiliency policyKey has been applied to a building block."),
	)
	if err != nil {
		return err
	}

	activationsCount, err := m.meter.Int64Counter(
		"resiliency.activations_total",
		metric.WithDescription("Number of times a resiliency policyKey has been activated in a building block after a failure or after a state change."),
	)
	if err != nil {
		return err
	}

	m.policiesLoadCount = policiesLoadCount
	m.executionCount = executionCount
	m.activationsCount = activationsCount
	m.enabled = true
	m.appID = id

	return nil
}

// PolicyLoaded records metric when policy is loaded.
func (m *resiliencyMetrics) PolicyLoaded(resiliencyName, namespace string) {
	if m.enabled {
		m.policiesLoadCount.Add(m.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, m.appID), attribute.String(resiliencyNameKey, resiliencyName), attribute.String(namespaceKey, namespace)))
	}
}

// PolicyWithStatusExecuted records metric when policy is executed with added status information (e.g., circuit breaker open).
func (m *resiliencyMetrics) PolicyWithStatusExecuted(resiliencyName, namespace string, policy PolicyType, flowDirection PolicyFlowDirection, target string, status string) {
	if m.enabled {
		m.executionCount.Add(m.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, m.appID), attribute.String(resiliencyNameKey, resiliencyName), attribute.String(policyKey, string(policy)), attribute.String(namespaceKey, namespace), attribute.String(flowDirectionKey, string(flowDirection)), attribute.String(targetKey, target), attribute.String(statusKey, status)))
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
		m.activationsCount.Add(m.ctx, 1, metric.WithAttributes(attribute.String(appIDKey, m.appID), attribute.String(resiliencyNameKey, resiliencyName), attribute.String(policyKey, string(policy)), attribute.String(namespaceKey, namespace), attribute.String(flowDirectionKey, string(flowDirection)), attribute.String(targetKey, target), attribute.String(statusKey, status)))
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
