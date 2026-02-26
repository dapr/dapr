package diagnostics

import (
	"context"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/resiliency/breaker"
)

var (
	CircuitBreakerPolicy PolicyType = "circuitbreaker"
	RetryPolicy          PolicyType = "retry"
	TimeoutPolicy        PolicyType = "timeout"

	OutboundPolicyFlowDirection PolicyFlowDirection = "outbound"
	InboundPolicyFlowDirection  PolicyFlowDirection = "inbound"

	cbStatuses = []string{
		string(breaker.StateClosed),
		string(breaker.StateHalfOpen),
		string(breaker.StateOpen),
		string(breaker.StateUnknown),
	}
)

type PolicyType string

type PolicyFlowDirection string

type resiliencyMetrics struct {
	policiesLoadCount   *stats.Int64Measure
	executionCount      *stats.Int64Measure
	activationsCount    *stats.Int64Measure
	circuitbreakerState *stats.Int64Measure

	appID   string
	enabled bool
	meter   stats.Recorder
	baseCtx context.Context
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
		circuitbreakerState: stats.Int64(
			"resiliency/cb_state",
			"A resiliency policy's current CircuitBreakerState state. 0 is closed, 1 is half-open, 2 is open, and -1 is unknown.",
			stats.UnitDimensionless),
		enabled: false,
	}
}

// Init registers the resiliency metrics views.
func (m *resiliencyMetrics) Init(meter view.Meter, id string) error {
	m.enabled = true
	m.appID = id
	m.meter = meter
	// Pre-tag the context with app_id once to reduce repeated tag.Upsert allocations.
	// Namespace is provided per-call because this Init method does not receive it.
	m.baseCtx, _ = tag.New(context.Background(), tag.Upsert(appIDKey, id))

	return meter.Register(
		diagUtils.NewMeasureView(m.policiesLoadCount, []tag.Key{appIDKey, resiliencyNameKey, namespaceKey}, view.Count()),
		diagUtils.NewMeasureView(m.executionCount, []tag.Key{appIDKey, resiliencyNameKey, policyKey, namespaceKey, flowDirectionKey, targetKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(m.activationsCount, []tag.Key{appIDKey, resiliencyNameKey, policyKey, namespaceKey, flowDirectionKey, targetKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(m.circuitbreakerState, []tag.Key{appIDKey, resiliencyNameKey, policyKey, namespaceKey, flowDirectionKey, targetKey, statusKey}, view.LastValue()),
	)
}

// PolicyLoaded records metric when policy is loaded.
func (m *resiliencyMetrics) PolicyLoaded(resiliencyName, namespace string) {
	if !m.enabled {
		return
	}
	_ = stats.RecordWithOptions(m.baseCtx,
		stats.WithRecorder(m.meter),
		stats.WithTags(tag.Upsert(resiliencyNameKey, resiliencyName), tag.Upsert(namespaceKey, namespace)),
		stats.WithMeasurements(m.policiesLoadCount.M(1)))
}

// PolicyWithStatusExecuted records metric when policy is executed with added status information (e.g., circuit breaker open).
func (m *resiliencyMetrics) PolicyWithStatusExecuted(resiliencyName, namespace string, policy PolicyType, flowDirection PolicyFlowDirection, target string, status string) {
	if !m.enabled {
		return
	}
	policyStr := string(policy)
	flowStr := string(flowDirection)

	// Record count metric for all resiliency executions
	_ = stats.RecordWithOptions(m.baseCtx,
		stats.WithRecorder(m.meter),
		stats.WithTags(
			tag.Upsert(resiliencyNameKey, resiliencyName),
			tag.Upsert(policyKey, policyStr),
			tag.Upsert(namespaceKey, namespace),
			tag.Upsert(flowDirectionKey, flowStr),
			tag.Upsert(targetKey, target),
			tag.Upsert(statusKey, status)),
		stats.WithMeasurements(m.executionCount.M(1)))

	// Record cb gauge, 4 metrics, one for each cb state, with the active state having a value of 1, otherwise 0
	if policy == CircuitBreakerPolicy {
		for _, s := range cbStatuses {
			val := int64(0)
			if s == status {
				val = 1
			}
			_ = stats.RecordWithOptions(m.baseCtx,
				stats.WithRecorder(m.meter),
				stats.WithTags(
					tag.Upsert(resiliencyNameKey, resiliencyName),
					tag.Upsert(policyKey, policyStr),
					tag.Upsert(namespaceKey, namespace),
					tag.Upsert(flowDirectionKey, flowStr),
					tag.Upsert(targetKey, target),
					tag.Upsert(statusKey, s)),
				stats.WithMeasurements(m.circuitbreakerState.M(val)))
		}
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

// PolicyWithStatusActivated records metrics when policy is activated after a failure or in the case of circuit breaker after a state change. with added state/status (e.g., circuit breaker open).
func (m *resiliencyMetrics) PolicyWithStatusActivated(resiliencyName, namespace string, policy PolicyType, flowDirection PolicyFlowDirection, target string, status string) {
	if !m.enabled {
		return
	}
	_ = stats.RecordWithOptions(m.baseCtx,
		stats.WithRecorder(m.meter),
		stats.WithTags(
			tag.Upsert(resiliencyNameKey, resiliencyName),
			tag.Upsert(policyKey, string(policy)),
			tag.Upsert(namespaceKey, namespace),
			tag.Upsert(flowDirectionKey, string(flowDirection)),
			tag.Upsert(targetKey, target),
			tag.Upsert(statusKey, status)),
		stats.WithMeasurements(m.activationsCount.M(1)))
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
