package diagnostics

import (
	"context"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	processStatusKey = "process_status"
	successKey       = "success"
	topicKey         = "topic"
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
	CryptoOp                 = "crypto_op"
)

// componentMetrics holds dapr runtime metrics for components.
type componentMetrics struct {
	meter metric.Meter

	pubsubIngressCount          metric.Int64Counter
	pubsubIngressLatency        metric.Float64Histogram
	bulkPubsubIngressCount      metric.Int64Counter
	bulkPubsubEventIngressCount metric.Int64Counter
	bulkPubsubIngressLatency    metric.Float64Histogram
	pubsubEgressCount           metric.Int64Counter
	pubsubEgressLatency         metric.Float64Histogram
	bulkPubsubEgressCount       metric.Int64Counter
	bulkPubsubEventEgressCount  metric.Int64Counter
	bulkPubsubEgressLatency     metric.Float64Histogram

	inputBindingCount    metric.Int64Counter
	inputBindingLatency  metric.Float64Histogram
	outputBindingCount   metric.Int64Counter
	outputBindingLatency metric.Float64Histogram

	stateCount   metric.Int64Counter
	stateLatency metric.Float64Histogram

	configurationCount   metric.Int64Counter
	configurationLatency metric.Float64Histogram

	secretCount   metric.Int64Counter
	secretLatency metric.Float64Histogram

	cryptoCount   metric.Int64Counter
	cryptoLatency metric.Float64Histogram

	appID     string
	enabled   bool
	namespace string
}

// newComponentMetrics returns a componentMetrics instance with default stats.
func newComponentMetrics() *componentMetrics {
	m := otel.Meter("component")

	return &componentMetrics{
		meter:   m,
		enabled: false,
	}
}

// Init registers the component metrics views.
func (c *componentMetrics) Init(appID, namespace string) error {
	pubsubIngressLatency, err := c.meter.Float64Histogram(
		"component.pubsub_ingress.latencies",
		metric.WithDescription("The consuming app event processing latency."),
	)
	if err != nil {
		return err
	}

	pubsubIngressCount, err := c.meter.Int64Counter(
		"component.pubsub_ingress.count",
		metric.WithDescription("The number of incoming messages arriving from the pub/sub component."),
	)
	if err != nil {
		return err
	}

	bulkPubsubIngressLatency, err := c.meter.Float64Histogram(
		"component.pubsub_ingress.bulk.latencies",
		metric.WithDescription("The consuming app event processing latency for the bulk pub/sub component."),
	)
	if err != nil {
		return err
	}

	bulkPubsubIngressCount, err := c.meter.Int64Counter(
		"component.pubsub_ingress.bulk.count",
		metric.WithDescription("The number of incoming bulk subscribe calls arriving from the bulk pub/sub component."),
	)
	if err != nil {
		return err
	}

	bulkPubsubEventIngressCount, err := c.meter.Int64Counter(
		"component.pubsub_ingress.bulk.event_count",
		metric.WithDescription("Total number of incoming messages arriving from the bulk pub/sub component via Bulk Subscribe."),
	)
	if err != nil {
		return err
	}

	pubsubEgressLatency, err := c.meter.Float64Histogram(
		"component.pubsub_egress.latencies",
		metric.WithDescription("The latency of the response from the pub/sub component."),
	)
	if err != nil {
		return err
	}

	pubsubEgressCount, err := c.meter.Int64Counter(
		"component.pubsub_egress.count",
		metric.WithDescription("The number of outgoing messages published to the pub/sub component."),
	)
	if err != nil {
		return err
	}

	bulkPubsubEgressLatency, err := c.meter.Float64Histogram(
		"component.pubsub_egress.bulk.latencies",
		metric.WithDescription("The latency of the response for the bulk publish call from the pub/sub component."),
	)
	if err != nil {
		return err
	}

	bulkPubsubEgressCount, err := c.meter.Int64Counter(
		"component.pubsub_egress.bulk.count",
		metric.WithDescription("The number of bulk publish calls to the pub/sub component."),
	)
	if err != nil {
		return err
	}

	bulkPubsubEventEgressCount, err := c.meter.Int64Counter(
		"component.pubsub_egress.bulk.event_count",
		metric.WithDescription("The number of outgoing messages to the pub/sub component published through bulk publish API."),
	)
	if err != nil {
		return err
	}

	inputBindingLatency, err := c.meter.Float64Histogram(
		"component.input_binding.latencies",
		metric.WithDescription("The triggered app event processing latency."),
	)
	if err != nil {
		return err
	}

	inputBindingCount, err := c.meter.Int64Counter(
		"component.input_binding.count",
		metric.WithDescription("The number of incoming events arriving from the input binding component."),
	)
	if err != nil {
		return err
	}

	outputBindingLatency, err := c.meter.Float64Histogram(
		"component.output_binding.latencies",
		metric.WithDescription("The latency of the response from the output binding component."),
	)
	if err != nil {
		return err
	}

	outputBindingCount, err := c.meter.Int64Counter(
		"component.output_binding.count",
		metric.WithDescription("The number of operations invoked on the output binding component."),
	)
	if err != nil {
		return err
	}

	stateLatency, err := c.meter.Float64Histogram(
		"component.state.latencies",
		metric.WithDescription("The latency of the response from the state component."),
	)
	if err != nil {
		return err
	}

	stateCount, err := c.meter.Int64Counter(
		"component.state.count",
		metric.WithDescription("The number of operations performed on the state component."),
	)
	if err != nil {
		return err
	}

	configurationLatency, err := c.meter.Float64Histogram(
		"component.configuration.latencies",
		metric.WithDescription("The latency of the response from the configuration component."),
	)
	if err != nil {
		return err
	}

	configurationCount, err := c.meter.Int64Counter(
		"component.configuration.count",
		metric.WithDescription("The number of operations performed on the configuration component."),
	)
	if err != nil {
		return err
	}

	secretLatency, err := c.meter.Float64Histogram(
		"component.secret.latencies",
		metric.WithDescription("The latency of the response from the secret component."),
	)
	if err != nil {
		return err
	}

	secretCount, err := c.meter.Int64Counter(
		"component.secret.count",
		metric.WithDescription("The number of operations performed on the secret component."),
	)
	if err != nil {
		return err
	}

	cryptoLatency, err := c.meter.Float64Histogram(
		"component.crypto.latencies",
		metric.WithDescription("The latency of the response from the crypto component."),
	)
	if err != nil {
		return err
	}

	cryptoCount, err := c.meter.Int64Counter(
		"component.crypto.count",
		metric.WithDescription("The number of operations performed on the crypto component."),
	)
	if err != nil {
		return err
	}

	c.pubsubIngressLatency = pubsubIngressLatency
	c.pubsubIngressCount = pubsubIngressCount
	c.bulkPubsubIngressLatency = bulkPubsubIngressLatency
	c.bulkPubsubIngressCount = bulkPubsubIngressCount
	c.bulkPubsubEventIngressCount = bulkPubsubEventIngressCount
	c.pubsubEgressLatency = pubsubEgressLatency
	c.pubsubEgressCount = pubsubEgressCount
	c.bulkPubsubEgressLatency = bulkPubsubEgressLatency
	c.bulkPubsubEgressCount = bulkPubsubEgressCount
	c.bulkPubsubEventEgressCount = bulkPubsubEventEgressCount
	c.inputBindingLatency = inputBindingLatency
	c.inputBindingCount = inputBindingCount
	c.outputBindingLatency = outputBindingLatency
	c.outputBindingCount = outputBindingCount
	c.stateLatency = stateLatency
	c.stateCount = stateCount
	c.configurationLatency = configurationLatency
	c.configurationCount = configurationCount
	c.secretLatency = secretLatency
	c.secretCount = secretCount
	c.cryptoLatency = cryptoLatency
	c.cryptoCount = cryptoCount
	c.appID = appID
	c.enabled = true
	c.namespace = namespace

	return nil
}

// PubsubIngressEvent records the metrics for a pub/sub ingress event.
func (c *componentMetrics) PubsubIngressEvent(ctx context.Context, component, processStatus, topic string, elapsed float64) {
	if c.enabled {
		c.pubsubIngressCount.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(processStatusKey, processStatus), attribute.String(topicKey, topic)))

		if elapsed > 0 {
			c.pubsubIngressLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(processStatusKey, processStatus), attribute.String(topicKey, topic)))
		}
	}
}

// BulkPubsubIngressEvent records the metrics for a bulk pub/sub ingress event.
func (c *componentMetrics) BulkPubsubIngressEvent(ctx context.Context, component, topic string, elapsed float64) {
	if c.enabled {
		c.bulkPubsubIngressCount.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(topicKey, topic)))

		if elapsed > 0 {
			c.bulkPubsubIngressLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(topicKey, topic)))
		}
	}
}

// BulkPubsubIngressEventEntries records the metrics for entries inside a bulk pub/sub ingress event.
func (c *componentMetrics) BulkPubsubIngressEventEntries(ctx context.Context, component, topic string, processStatus string, eventCount int64) {
	if c.enabled && eventCount > 0 {
		c.bulkPubsubEventIngressCount.Add(ctx, eventCount, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(processStatusKey, processStatus), attribute.String(topicKey, topic)))
	}
}

// BulkPubsubEgressEvent records the metris for a pub/sub egress event.
// eventCount if greater than zero implies successful publish of few/all events in the bulk publish call
func (c *componentMetrics) BulkPubsubEgressEvent(ctx context.Context, component, topic string, success bool, eventCount int64, elapsed float64) {
	if c.enabled {
		c.bulkPubsubEgressCount.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(successKey, strconv.FormatBool(success)), attribute.String(topicKey, topic)))

		if eventCount > 0 {
			// There is at leaset one success in the bulk publish call even if overall success of the call might be a failure
			c.bulkPubsubEventEgressCount.Add(ctx, eventCount, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(successKey, strconv.FormatBool(success)), attribute.String(topicKey, topic)))
		}
		if elapsed > 0 {
			c.bulkPubsubEgressLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(successKey, strconv.FormatBool(success)), attribute.String(topicKey, topic)))
		}
	}
}

// PubsubEgressEvent records the metris for a pub/sub egress event.
func (c *componentMetrics) PubsubEgressEvent(ctx context.Context, component, topic string, success bool, elapsed float64) {
	if c.enabled {
		c.pubsubEgressCount.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(successKey, strconv.FormatBool(success)), attribute.String(topicKey, topic)))

		if elapsed > 0 {
			c.pubsubEgressLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(successKey, strconv.FormatBool(success)), attribute.String(topicKey, topic)))
		}
	}
}

// InputBindingEvent records the metrics for an input binding event.
func (c *componentMetrics) InputBindingEvent(ctx context.Context, component string, success bool, elapsed float64) {
	if c.enabled {
		c.inputBindingCount.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(successKey, strconv.FormatBool(success))))

		if elapsed > 0 {
			c.inputBindingLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(successKey, strconv.FormatBool(success))))
		}
	}
}

// OutputBindingEvent records the metrics for an output binding event.
func (c *componentMetrics) OutputBindingEvent(ctx context.Context, component, operation string, success bool, elapsed float64) {
	if c.enabled {
		c.outputBindingCount.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(operationKey, operation), attribute.String(successKey, strconv.FormatBool(success))))

		if elapsed > 0 {
			c.outputBindingLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(operationKey, operation), attribute.String(successKey, strconv.FormatBool(success))))
		}
	}
}

// StateInvoked records the metrics for a state event.
func (c *componentMetrics) StateInvoked(ctx context.Context, component, operation string, success bool, elapsed float64) {
	if c.enabled {
		c.stateCount.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(operationKey, operation), attribute.String(successKey, strconv.FormatBool(success))))

		if elapsed > 0 {
			c.stateLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(operationKey, operation), attribute.String(successKey, strconv.FormatBool(success))))
		}
	}
}

// ConfigurationInvoked records the metrics for a configuration event.
func (c *componentMetrics) ConfigurationInvoked(ctx context.Context, component, operation string, success bool, elapsed float64) {
	if c.enabled {
		c.configurationCount.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(operationKey, operation), attribute.String(successKey, strconv.FormatBool(success))))

		if elapsed > 0 {
			c.configurationLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(operationKey, operation), attribute.String(successKey, strconv.FormatBool(success))))
		}
	}
}

// SecretInvoked records the metrics for a secret event.
func (c *componentMetrics) SecretInvoked(ctx context.Context, component, operation string, success bool, elapsed float64) {
	if c.enabled {
		c.secretCount.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(operationKey, operation), attribute.String(successKey, strconv.FormatBool(success))))

		if elapsed > 0 {
			c.secretLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(operationKey, operation), attribute.String(successKey, strconv.FormatBool(success))))
		}
	}
}

// CryptoInvoked records the metrics for a crypto event.
func (c *componentMetrics) CryptoInvoked(ctx context.Context, component, operation string, success bool, elapsed float64) {
	if c.enabled {
		c.cryptoCount.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(operationKey, operation), attribute.String(successKey, strconv.FormatBool(success))))

		if elapsed > 0 {
			c.cryptoLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, c.appID), attribute.String(componentKey, component), attribute.String(namespaceKey, c.namespace), attribute.String(operationKey, operation), attribute.String(successKey, strconv.FormatBool(success))))
		}
	}
}

func ElapsedSince(start time.Time) float64 {
	return float64(time.Since(start) / time.Millisecond)
}
