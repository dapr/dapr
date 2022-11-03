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
	"context"
	"time"

	isemconv "github.com/dapr/dapr/pkg/diagnostics/semconv"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/metric/instrument/syncfloat64"
	"go.opentelemetry.io/otel/metric/instrument/syncint64"
	"go.opentelemetry.io/otel/metric/unit"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
)

const (
	Delete                   = "delete"
	Get                      = "get"
	Set                      = "set"
	StateQuery               = "query"
	ConfigurationSubscribe   = "subscribe"
	ConfigurationUnsubscribe = "unsubscribe"
	StateTransaction         = "transaction"
	BulkGet                  = "bulk_get"
	BulkDelete               = "bulk_delete"
)

// componentMetrics holds dapr runtime metrics for components.
type componentMetrics struct {
	pubsubIngressCount          syncint64.Counter
	pubsubIngressLatency        syncfloat64.Histogram
	bulkPubsubIngressCount      syncint64.Counter
	bulkPubsubEventIngressCount syncint64.Counter
	bulkPubsubIngressLatency    syncfloat64.Histogram
	pubsubEgressCount           syncint64.Counter
	pubsubEgressLatency         syncfloat64.Histogram
	bulkPubsubEgressCount       syncint64.Counter
	bulkPubsubEventEgressCount  syncint64.Counter
	bulkPubsubEgressLatency     syncfloat64.Histogram

	inputBindingCount    syncint64.Counter
	inputBindingLatency  syncfloat64.Histogram
	outputBindingCount   syncint64.Counter
	outputBindingLatency syncfloat64.Histogram

	stateCount   syncint64.Counter
	stateLatency syncfloat64.Histogram

	configurationCount   syncint64.Counter
	configurationLatency syncfloat64.Histogram

	secretCount   syncint64.Counter
	secretLatency syncfloat64.Histogram
}

// newComponentMetrics returns a componentMetrics instance with default stats.
func (m *MetricClient) newComponentMetrics() *componentMetrics {
	cm := new(componentMetrics)
	cm.pubsubIngressCount, _ = m.meter.SyncInt64().Counter(
		"component/pubsub_ingress/count",
		instrument.WithDescription("The number of incoming messages arriving from the pub/sub component."),
		instrument.WithUnit(unit.Dimensionless))
	cm.pubsubIngressLatency, _ = m.meter.SyncFloat64().Histogram(
		"component/pubsub_ingress/latencies",
		instrument.WithDescription("The consuming app event processing latency."),
		instrument.WithUnit(unit.Milliseconds))
	cm.bulkPubsubIngressCount, _ = m.meter.SyncInt64().Counter(
		"component/pubsub_ingress/bulk/count",
		instrument.WithDescription("The number of incoming bulk subscribe calls arriving from the bulk pub/sub component."),
		instrument.WithUnit(unit.Dimensionless))
	cm.bulkPubsubEventIngressCount, _ = m.meter.SyncInt64().Counter(
		"component/pubsub_ingress/bulk/event_count",
		instrument.WithDescription("Total number of incoming messages arriving from the bulk pub/sub component via Bulk Subscribe."),
		instrument.WithUnit(unit.Dimensionless))
	cm.bulkPubsubIngressLatency, _ = m.meter.SyncFloat64().Histogram(
		"component/pubsub_ingress/bulk/latencies",
		instrument.WithDescription("The consuming app event processing latency for the bulk pub/sub component."),
		instrument.WithUnit(unit.Milliseconds))
	cm.pubsubEgressCount, _ = m.meter.SyncInt64().Counter(
		"component/pubsub_egress/count",
		instrument.WithDescription("The number of outgoing messages published to the pub/sub component."),
		instrument.WithUnit(unit.Dimensionless))
	cm.pubsubEgressLatency, _ = m.meter.SyncFloat64().Histogram(
		"component/pubsub_egress/latencies",
		instrument.WithDescription("The latency of the response from the pub/sub component."),
		instrument.WithUnit(unit.Milliseconds))
	cm.bulkPubsubEgressCount, _ = m.meter.SyncInt64().Counter(
		"component/pubsub_egress/bulk/count",
		instrument.WithDescription("The number of bulk publish calls to the pub/sub component."),
		instrument.WithUnit(unit.Dimensionless))
	cm.bulkPubsubEventEgressCount, _ = m.meter.SyncInt64().Counter(
		"component/pubsub_egress/bulk/event_count",
		instrument.WithDescription("The number of outgoing messages to the pub/sub component published through bulk publish API."),
		instrument.WithUnit(unit.Dimensionless))
	cm.bulkPubsubEgressLatency, _ = m.meter.SyncFloat64().Histogram(
		"component/pubsub_egress/bulk/latencies",
		instrument.WithDescription("The latency of the response for the bulk publish call from the pub/sub component."),
		instrument.WithUnit(unit.Milliseconds))
	cm.inputBindingCount, _ = m.meter.SyncInt64().Counter(
		"component/input_binding/count",
		instrument.WithDescription("The number of incoming events arriving from the input binding component."),
		instrument.WithUnit(unit.Dimensionless))
	cm.inputBindingLatency, _ = m.meter.SyncFloat64().Histogram(
		"component/input_binding/latencies",
		instrument.WithDescription("The triggered app event processing latency."),
		instrument.WithUnit(unit.Milliseconds))
	cm.outputBindingCount, _ = m.meter.SyncInt64().Counter(
		"component/output_binding/count",
		instrument.WithDescription("The number of operations invoked on the output binding component."),
		instrument.WithUnit(unit.Dimensionless))
	cm.outputBindingLatency, _ = m.meter.SyncFloat64().Histogram(
		"component/output_binding/latencies",
		instrument.WithDescription("The latency of the response from the output binding component."),
		instrument.WithUnit(unit.Milliseconds))
	cm.stateCount, _ = m.meter.SyncInt64().Counter(
		"component/state/count",
		instrument.WithDescription("The number of operations performed on the state component."),
		instrument.WithUnit(unit.Dimensionless))
	cm.stateLatency, _ = m.meter.SyncFloat64().Histogram(
		"component/state/latencies",
		instrument.WithDescription("The latency of the response from the state component."),
		instrument.WithUnit(unit.Milliseconds))
	cm.configurationCount, _ = m.meter.SyncInt64().Counter(
		"component/configuration/count",
		instrument.WithDescription("The number of operations performed on the configuration component."),
		instrument.WithUnit(unit.Dimensionless))
	cm.configurationLatency, _ = m.meter.SyncFloat64().Histogram(
		"component/configuration/latencies",
		instrument.WithDescription("The latency of the response from the configuration component."),
		instrument.WithUnit(unit.Milliseconds))
	cm.secretCount, _ = m.meter.SyncInt64().Counter(
		"component/secret/count",
		instrument.WithDescription("The number of operations performed on the secret component."),
		instrument.WithUnit(unit.Dimensionless))
	cm.secretLatency, _ = m.meter.SyncFloat64().Histogram(
		"component/secret/latencies",
		instrument.WithDescription("The latency of the response from the secret component."),
		instrument.WithUnit(unit.Milliseconds))
	return cm
}

// PubsubIngressEvent records the metrics for a pub/sub ingress event.
func (c *componentMetrics) PubsubIngressEvent(ctx context.Context, component, processStatus, topic string, elapsed float64) {
	if c == nil {
		return
	}
	attributes := []attribute.KeyValue{
		isemconv.ComponentNameKey.String(component),
		isemconv.ComponentProcessStatusKey.String(processStatus),
		isemconv.ComponentTopicKey.String(topic),
	}
	c.pubsubIngressCount.Add(ctx, 1, attributes...)

	if elapsed > 0 {
		c.pubsubIngressLatency.Record(ctx, elapsed, attributes...)
	}
}

// BulkPubsubIngressEvent records the metrics for a bulk pub/sub ingress event.
func (c *componentMetrics) BulkPubsubIngressEvent(ctx context.Context, component, topic string, elapsed float64) {
	if c == nil {
		return
	}
	attributes := []attribute.KeyValue{
		isemconv.ComponentNameKey.String(component),
		isemconv.ComponentTopicKey.String(topic),
	}
	c.bulkPubsubIngressCount.Add(ctx, 1, attributes...)

	if elapsed > 0 {
		c.bulkPubsubIngressLatency.Record(ctx, elapsed, attributes...)
	}
}

// BulkPubsubIngressEventEntries records the metrics for entries inside a bulk pub/sub ingress event.
func (c *componentMetrics) BulkPubsubIngressEventEntries(ctx context.Context, component, topic string, processStatus string, eventCount int64) {
	if c == nil {
		return
	}
	if eventCount > 0 {
		attributes := []attribute.KeyValue{
			isemconv.ComponentNameKey.String(component),
			isemconv.ComponentProcessStatusKey.String(processStatus),
			isemconv.ComponentTopicKey.String(topic),
		}
		c.bulkPubsubEventIngressCount.Add(ctx, eventCount, attributes...)
	}
}

// BulkPubsubEgressEvent records the metris for a pub/sub egress event.
// eventCount if greater than zero implies successful publish of few/all events in the bulk publish call
func (c *componentMetrics) BulkPubsubEgressEvent(ctx context.Context, component, topic string, success bool, eventCount int64, elapsed float64) {
	if c == nil {
		return
	}

	attributes := []attribute.KeyValue{
		isemconv.ComponentNameKey.String(component),
		isemconv.ComponentTopicKey.String(topic),
		isemconv.ComponentSuccessKey.Bool(success),
	}

	c.bulkPubsubEgressCount.Add(ctx, 1, attributes...)
	if eventCount > 0 {
		// There is at leaset one success in the bulk publish call even if overall success of the call might be a failure
		c.bulkPubsubEventEgressCount.Add(ctx, eventCount, attributes...)
	}
	if elapsed > 0 {
		c.bulkPubsubEgressLatency.Record(ctx, elapsed, attributes...)
	}
}

// PubsubEgressEvent records the metris for a pub/sub egress event.
func (c *componentMetrics) PubsubEgressEvent(ctx context.Context, component, topic string, success bool, elapsed float64) {
	if c == nil {
		return
	}
	attributes := []attribute.KeyValue{
		isemconv.ComponentNameKey.String(component),
		isemconv.ComponentTopicKey.String(topic),
		isemconv.ComponentSuccessKey.Bool(success),
	}
	c.pubsubEgressCount.Add(ctx, 1, attributes...)
	if elapsed > 0 {
		c.pubsubEgressLatency.Record(ctx, elapsed, attributes...)
	}
}

// InputBindingEvent records the metrics for an input binding event.
func (c *componentMetrics) InputBindingEvent(ctx context.Context, component string, success bool, elapsed float64) {
	if c == nil {
		return
	}
	attrs := []attribute.KeyValue{
		isemconv.ComponentNameKey.String(component),
		isemconv.ComponentSuccessKey.Bool(success),
	}
	c.inputBindingCount.Add(ctx, 1, attrs...)

	if elapsed > 0 {
		c.inputBindingLatency.Record(ctx, elapsed, attrs...)
	}
}

// OutputBindingEvent records the metrics for an output binding event.
func (c *componentMetrics) OutputBindingEvent(ctx context.Context, component, operation string, success bool, elapsed float64) {
	if c == nil {
		return
	}
	attrs := []attribute.KeyValue{
		isemconv.ComponentNameKey.String(component),
		isemconv.ComponentOperationKey.String(operation),
		isemconv.ComponentSuccessKey.Bool(success),
	}
	c.outputBindingCount.Add(ctx, 1, attrs...)

	if elapsed > 0 {
		c.outputBindingLatency.Record(ctx, elapsed, attrs...)
	}
}

// StateInvoked records the metrics for a state event.
func (c *componentMetrics) StateInvoked(ctx context.Context, component, operation string, success bool, elapsed float64) {
	if c == nil {
		return
	}
	attrs := []attribute.KeyValue{
		isemconv.ComponentNameKey.String(component),
		isemconv.ComponentOperationKey.String(operation),
		isemconv.ComponentSuccessKey.Bool(success),
	}
	c.stateCount.Add(ctx, 1, attrs...)

	if elapsed > 0 {
		c.stateLatency.Record(ctx, elapsed, attrs...)
	}
}

// ConfigurationInvoked records the metrics for a configuration event.
func (c *componentMetrics) ConfigurationInvoked(ctx context.Context, component, operation string, success bool, elapsed float64) {
	if c == nil {
		return
	}
	attrs := []attribute.KeyValue{
		isemconv.ComponentNameKey.String(component),
		isemconv.ComponentOperationKey.String(operation),
		isemconv.ComponentSuccessKey.Bool(success),
	}
	c.configurationCount.Add(ctx, 1, attrs...)

	if elapsed > 0 {
		c.configurationLatency.Record(ctx, elapsed, attrs...)
	}
}

// SecretInvoked records the metrics for a secret event.
func (c *componentMetrics) SecretInvoked(ctx context.Context, component, operation string, success bool, elapsed float64) {
	if c == nil {
		return
	}
	attrs := []attribute.KeyValue{
		isemconv.ComponentNameKey.String(component),
		isemconv.ComponentOperationKey.String(operation),
		isemconv.ComponentSuccessKey.Bool(success),
	}
	c.secretCount.Add(ctx, 1, attrs...)

	if elapsed > 0 {
		c.secretLatency.Record(ctx, elapsed, attrs...)
	}
}

func ElapsedSince(start time.Time) float64 {
	return float64(time.Since(start) / time.Millisecond)
}

// InputComponentBindings builds attrs.
func InputComponentBindings(name, url string) []attribute.KeyValue {
	return []attribute.KeyValue{
		isemconv.ComponentNameKey.String(name),
		semconv.RPCServiceKey.String(daprRPCDaprService),
		semconv.RPCMethodKey.String(url),
		isemconv.ComponentBindings,
	}
}

// Subscriptions builds attrs.
func Subscriptions(topic string) []attribute.KeyValue {
	return []attribute.KeyValue{
		isemconv.ComponentPubsub,
		semconv.MessagingDestinationKey.String(topic),
		semconv.MessagingDestinationKindTopic,
	}
}
