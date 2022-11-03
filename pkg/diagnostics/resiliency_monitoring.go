package diagnostics

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/metric/instrument/syncint64"
	"go.opentelemetry.io/otel/metric/unit"

	isemconv "github.com/dapr/dapr/pkg/diagnostics/semconv"
)

var (
	CircuitBreakerPolicy PolicyType = "circuitbreaker"
	RetryPolicy          PolicyType = "retry"
	TimeoutPolicy        PolicyType = "timeout"
)

type PolicyType string

type resiliencyMetrics struct {
	policiesLoadCount syncint64.Counter
	executionCount    syncint64.Counter
}

func (m *MetricClient) newResiliencyMetrics() *resiliencyMetrics {
	rm := new(resiliencyMetrics)
	rm.policiesLoadCount, _ = m.meter.SyncInt64().Counter(
		"resiliency/loaded",
		instrument.WithDescription("The number of incoming messages arriving from the pub/sub component."),
		instrument.WithUnit(unit.Dimensionless))
	rm.executionCount, _ = m.meter.SyncInt64().Counter(
		"resiliency/count",
		instrument.WithDescription("Number of times a resiliency policyKey has been executed."),
		instrument.WithUnit(unit.Dimensionless))

	return rm
}

// PolicyLoaded records metric when policy is loaded.
func (m *resiliencyMetrics) PolicyLoaded(resiliencyName string) {
	if m == nil {
		return
	}

	attributes := []attribute.KeyValue{
		isemconv.ResiliencyNameKey.String(resiliencyName),
	}

	m.policiesLoadCount.Add(context.Background(), 1, attributes...)
}

// PolicyExecuted records metric when policy is executed.
func (m *resiliencyMetrics) PolicyExecuted(ctx context.Context, resiliencyName string, policy PolicyType) {
	if m == nil {
		return
	}

	attributes := []attribute.KeyValue{
		isemconv.ResiliencyNameKey.String(resiliencyName),
		isemconv.ResiliencyPolicyKey.String(string(policy)),
	}

	m.executionCount.Add(ctx, 1, attributes...)
}
