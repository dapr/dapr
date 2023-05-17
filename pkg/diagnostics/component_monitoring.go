package diagnostics

import (
	"context"
	"strconv"
	"time"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/diagnostics/utils"
)

var (
	processStatusKey = tag.MustNewKey("process_status")
	successKey       = tag.MustNewKey("success")
	topicKey         = tag.MustNewKey("topic")
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
	pubsubIngressCount          *stats.Int64Measure
	pubsubIngressLatency        *stats.Float64Measure
	bulkPubsubIngressCount      *stats.Int64Measure
	bulkPubsubEventIngressCount *stats.Int64Measure
	bulkPubsubIngressLatency    *stats.Float64Measure
	pubsubEgressCount           *stats.Int64Measure
	pubsubEgressLatency         *stats.Float64Measure
	bulkPubsubEgressCount       *stats.Int64Measure
	bulkPubsubEventEgressCount  *stats.Int64Measure
	bulkPubsubEgressLatency     *stats.Float64Measure

	inputBindingCount    *stats.Int64Measure
	inputBindingLatency  *stats.Float64Measure
	outputBindingCount   *stats.Int64Measure
	outputBindingLatency *stats.Float64Measure

	stateCount   *stats.Int64Measure
	stateLatency *stats.Float64Measure

	configurationCount   *stats.Int64Measure
	configurationLatency *stats.Float64Measure

	secretCount   *stats.Int64Measure
	secretLatency *stats.Float64Measure

	cryptoCount   *stats.Int64Measure
	cryptoLatency *stats.Float64Measure

	regRules  utils.Rules
	meter     view.Meter
	appID     string
	enabled   bool
	namespace string
}

// newComponentMetrics returns a componentMetrics instance with default stats.
func newComponentMetrics(meter view.Meter, regRules utils.Rules) *componentMetrics {
	return &componentMetrics{
		meter:    meter,
		regRules: regRules,
		pubsubIngressCount: stats.Int64(
			"component/pubsub_ingress/count",
			"The number of incoming messages arriving from the pub/sub component.",
			stats.UnitDimensionless),
		pubsubIngressLatency: stats.Float64(
			"component/pubsub_ingress/latencies",
			"The consuming app event processing latency.",
			stats.UnitMilliseconds),
		bulkPubsubIngressCount: stats.Int64(
			"component/pubsub_ingress/bulk/count",
			"The number of incoming bulk subscribe calls arriving from the bulk pub/sub component.",
			stats.UnitDimensionless),
		bulkPubsubEventIngressCount: stats.Int64(
			"component/pubsub_ingress/bulk/event_count",
			"Total number of incoming messages arriving from the bulk pub/sub component via Bulk Subscribe.",
			stats.UnitDimensionless),
		bulkPubsubIngressLatency: stats.Float64(
			"component/pubsub_ingress/bulk/latencies",
			"The consuming app event processing latency for the bulk pub/sub component.",
			stats.UnitMilliseconds),
		pubsubEgressCount: stats.Int64(
			"component/pubsub_egress/count",
			"The number of outgoing messages published to the pub/sub component.",
			stats.UnitDimensionless),
		pubsubEgressLatency: stats.Float64(
			"component/pubsub_egress/latencies",
			"The latency of the response from the pub/sub component.",
			stats.UnitMilliseconds),
		bulkPubsubEgressCount: stats.Int64(
			"component/pubsub_egress/bulk/count",
			"The number of bulk publish calls to the pub/sub component.",
			stats.UnitDimensionless),
		bulkPubsubEventEgressCount: stats.Int64(
			"component/pubsub_egress/bulk/event_count",
			"The number of outgoing messages to the pub/sub component published through bulk publish API.",
			stats.UnitDimensionless),
		bulkPubsubEgressLatency: stats.Float64(
			"component/pubsub_egress/bulk/latencies",
			"The latency of the response for the bulk publish call from the pub/sub component.",
			stats.UnitMilliseconds),
		inputBindingCount: stats.Int64(
			"component/input_binding/count",
			"The number of incoming events arriving from the input binding component.",
			stats.UnitDimensionless),
		inputBindingLatency: stats.Float64(
			"component/input_binding/latencies",
			"The triggered app event processing latency.",
			stats.UnitMilliseconds),
		outputBindingCount: stats.Int64(
			"component/output_binding/count",
			"The number of operations invoked on the output binding component.",
			stats.UnitDimensionless),
		outputBindingLatency: stats.Float64(
			"component/output_binding/latencies",
			"The latency of the response from the output binding component.",
			stats.UnitMilliseconds),
		stateCount: stats.Int64(
			"component/state/count",
			"The number of operations performed on the state component.",
			stats.UnitDimensionless),
		stateLatency: stats.Float64(
			"component/state/latencies",
			"The latency of the response from the state component.",
			stats.UnitMilliseconds),
		configurationCount: stats.Int64(
			"component/configuration/count",
			"The number of operations performed on the configuration component.",
			stats.UnitDimensionless),
		configurationLatency: stats.Float64(
			"component/configuration/latencies",
			"The latency of the response from the configuration component.",
			stats.UnitMilliseconds),
		secretCount: stats.Int64(
			"component/secret/count",
			"The number of operations performed on the secret component.",
			stats.UnitDimensionless),
		secretLatency: stats.Float64(
			"component/secret/latencies",
			"The latency of the response from the secret component.",
			stats.UnitMilliseconds),
		cryptoCount: stats.Int64(
			"component/crypto/count",
			"The number of operations performed on the crypto component.",
			stats.UnitDimensionless),
		cryptoLatency: stats.Float64(
			"component/crypto/latencies",
			"The latency of the response from the crypto component.",
			stats.UnitMilliseconds),
	}
}

// init registers the component metrics views.
func (c *componentMetrics) init(appID, namespace string) error {
	c.appID = appID
	c.enabled = true
	c.namespace = namespace

	return c.meter.Register(
		utils.NewMeasureView(c.pubsubIngressLatency, []tag.Key{appIDKey, componentKey, namespaceKey, processStatusKey, topicKey}, defaultLatencyDistribution()),
		utils.NewMeasureView(c.pubsubIngressCount, []tag.Key{appIDKey, componentKey, namespaceKey, processStatusKey, topicKey}, utils.Count()),
		utils.NewMeasureView(c.bulkPubsubIngressLatency, []tag.Key{appIDKey, componentKey, namespaceKey, processStatusKey, topicKey}, defaultLatencyDistribution()),
		utils.NewMeasureView(c.bulkPubsubIngressCount, []tag.Key{appIDKey, componentKey, namespaceKey, processStatusKey, topicKey}, utils.Count()),
		utils.NewMeasureView(c.bulkPubsubEventIngressCount, []tag.Key{appIDKey, componentKey, namespaceKey, processStatusKey, topicKey}, utils.Count()),
		utils.NewMeasureView(c.pubsubEgressLatency, []tag.Key{appIDKey, componentKey, namespaceKey, successKey, topicKey}, defaultLatencyDistribution()),
		utils.NewMeasureView(c.pubsubEgressCount, []tag.Key{appIDKey, componentKey, namespaceKey, successKey, topicKey}, utils.Count()),
		utils.NewMeasureView(c.inputBindingLatency, []tag.Key{appIDKey, componentKey, namespaceKey, successKey}, defaultLatencyDistribution()),
		utils.NewMeasureView(c.inputBindingCount, []tag.Key{appIDKey, componentKey, namespaceKey, successKey}, utils.Count()),
		utils.NewMeasureView(c.outputBindingLatency, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, successKey}, defaultLatencyDistribution()),
		utils.NewMeasureView(c.outputBindingCount, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, successKey}, utils.Count()),
		utils.NewMeasureView(c.stateLatency, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, successKey}, defaultLatencyDistribution()),
		utils.NewMeasureView(c.stateCount, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, successKey}, utils.Count()),
		utils.NewMeasureView(c.configurationLatency, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, successKey}, defaultLatencyDistribution()),
		utils.NewMeasureView(c.configurationCount, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, successKey}, utils.Count()),
		utils.NewMeasureView(c.secretLatency, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, successKey}, defaultLatencyDistribution()),
		utils.NewMeasureView(c.secretCount, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, successKey}, utils.Count()),
		utils.NewMeasureView(c.cryptoLatency, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, successKey}, defaultLatencyDistribution()),
		utils.NewMeasureView(c.cryptoCount, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, successKey}, utils.Count()),
	)
}

// PubsubIngressEvent records the metrics for a pub/sub ingress event.
func (c *componentMetrics) PubsubIngressEvent(ctx context.Context, component, processStatus, topic string, elapsed float64) {
	if c.enabled {
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(c.meter),
			c.regRules.WithTags(c.pubsubIngressCount.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, processStatusKey, processStatus, topicKey, topic),
			stats.WithMeasurements(c.pubsubIngressCount.M(1)),
		)

		if elapsed > 0 {
			stats.RecordWithOptions(
				ctx,
				stats.WithRecorder(c.meter),
				c.regRules.WithTags(c.pubsubIngressLatency.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, processStatusKey, processStatus, topicKey, topic),
				stats.WithMeasurements(c.pubsubIngressLatency.M(elapsed)),
			)
		}
	}
}

// BulkPubsubIngressEvent records the metrics for a bulk pub/sub ingress event.
func (c *componentMetrics) BulkPubsubIngressEvent(ctx context.Context, component, topic string, elapsed float64) {
	if c.enabled {
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(c.meter),
			c.regRules.WithTags(c.bulkPubsubIngressCount.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, topicKey, topic),
			stats.WithMeasurements(c.bulkPubsubIngressCount.M(1)),
		)

		if elapsed > 0 {
			stats.RecordWithOptions(
				ctx,
				stats.WithRecorder(c.meter),
				c.regRules.WithTags(c.bulkPubsubIngressLatency.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, topicKey, topic),
				stats.WithMeasurements(c.bulkPubsubIngressLatency.M(elapsed)),
			)
		}
	}
}

// BulkPubsubIngressEventEntries records the metrics for entries inside a bulk pub/sub ingress event.
func (c *componentMetrics) BulkPubsubIngressEventEntries(ctx context.Context, component, topic string, processStatus string, eventCount int64) {
	if c.enabled && eventCount > 0 {
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(c.meter),
			c.regRules.WithTags(c.bulkPubsubEventIngressCount.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, processStatusKey, processStatus, topicKey, topic),
			stats.WithMeasurements(c.bulkPubsubEventIngressCount.M(eventCount)),
		)
	}
}

// BulkPubsubEgressEvent records the metris for a pub/sub egress event.
// eventCount if greater than zero implies successful publish of few/all events in the bulk publish call
func (c *componentMetrics) BulkPubsubEgressEvent(ctx context.Context, component, topic string, success bool, eventCount int64, elapsed float64) {
	if c.enabled {
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(c.meter),
			c.regRules.WithTags(c.bulkPubsubEgressCount.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, successKey, strconv.FormatBool(success), topicKey, topic),
			stats.WithMeasurements(c.bulkPubsubEgressCount.M(1)),
		)
		if eventCount > 0 {
			// There is at leaset one success in the bulk publish call even if overall success of the call might be a failure
			stats.RecordWithOptions(
				ctx,
				stats.WithRecorder(c.meter),
				c.regRules.WithTags(c.bulkPubsubEventEgressCount.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, successKey, true, topicKey, topic),
				stats.WithMeasurements(c.bulkPubsubEventEgressCount.M(eventCount)),
			)
		}
		if elapsed > 0 {
			stats.RecordWithOptions(
				ctx,
				stats.WithRecorder(c.meter),
				c.regRules.WithTags(c.bulkPubsubEgressLatency.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, successKey, strconv.FormatBool(success), topicKey, topic),
				stats.WithMeasurements(c.bulkPubsubEgressLatency.M(elapsed)),
			)
		}
	}
}

// PubsubEgressEvent records the metris for a pub/sub egress event.
func (c *componentMetrics) PubsubEgressEvent(ctx context.Context, component, topic string, success bool, elapsed float64) {
	if c.enabled {
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(c.meter),
			c.regRules.WithTags(c.pubsubEgressCount.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, successKey, strconv.FormatBool(success), topicKey, topic),
			stats.WithMeasurements(c.pubsubEgressCount.M(1)),
		)

		if elapsed > 0 {
			stats.RecordWithOptions(
				ctx,
				stats.WithRecorder(c.meter),
				c.regRules.WithTags(c.pubsubEgressLatency.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, successKey, strconv.FormatBool(success), topicKey, topic),
				stats.WithMeasurements(c.pubsubEgressLatency.M(elapsed)),
			)
		}
	}
}

// InputBindingEvent records the metrics for an input binding event.
func (c *componentMetrics) InputBindingEvent(ctx context.Context, component string, success bool, elapsed float64) {
	if c.enabled {
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(c.meter),
			c.regRules.WithTags(c.inputBindingCount.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, successKey, strconv.FormatBool(success)),
			stats.WithMeasurements(c.inputBindingCount.M(1)),
		)

		if elapsed > 0 {
			stats.RecordWithOptions(
				ctx,
				stats.WithRecorder(c.meter),
				c.regRules.WithTags(c.inputBindingLatency.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, successKey, strconv.FormatBool(success)),
				stats.WithMeasurements(c.inputBindingLatency.M(elapsed)),
			)
		}
	}
}

// OutputBindingEvent records the metrics for an output binding event.
func (c *componentMetrics) OutputBindingEvent(ctx context.Context, component, operation string, success bool, elapsed float64) {
	if c.enabled {
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(c.meter),
			c.regRules.WithTags(c.outputBindingCount.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, operationKey, operation, successKey, strconv.FormatBool(success)),
			stats.WithMeasurements(c.outputBindingCount.M(1)),
		)

		if elapsed > 0 {
			stats.RecordWithOptions(
				ctx,
				stats.WithRecorder(c.meter),
				c.regRules.WithTags(c.outputBindingLatency.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, operationKey, operation, successKey, strconv.FormatBool(success)),
				stats.WithMeasurements(c.outputBindingLatency.M(elapsed)),
			)
		}
	}
}

// StateInvoked records the metrics for a state event.
func (c *componentMetrics) StateInvoked(ctx context.Context, component, operation string, success bool, elapsed float64) {
	if c.enabled {
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(c.meter),
			c.regRules.WithTags(c.stateCount.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, operationKey, operation, successKey, strconv.FormatBool(success)),
			stats.WithMeasurements(c.stateCount.M(1)),
		)

		if elapsed > 0 {
			stats.RecordWithOptions(
				ctx,
				stats.WithRecorder(c.meter),
				c.regRules.WithTags(c.stateLatency.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, operationKey, operation, successKey, strconv.FormatBool(success)),
				stats.WithMeasurements(c.stateLatency.M(elapsed)),
			)
		}
	}
}

// ConfigurationInvoked records the metrics for a configuration event.
func (c *componentMetrics) ConfigurationInvoked(ctx context.Context, component, operation string, success bool, elapsed float64) {
	if c.enabled {
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(c.meter),
			c.regRules.WithTags(c.configurationCount.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, operationKey, operation, successKey, strconv.FormatBool(success)),
			stats.WithMeasurements(c.configurationCount.M(1)),
		)

		if elapsed > 0 {
			stats.RecordWithOptions(
				ctx,
				stats.WithRecorder(c.meter),
				c.regRules.WithTags(c.configurationLatency.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, operationKey, operation, successKey, strconv.FormatBool(success)),
				stats.WithMeasurements(c.configurationLatency.M(elapsed)),
			)
		}
	}
}

// SecretInvoked records the metrics for a secret event.
func (c *componentMetrics) SecretInvoked(ctx context.Context, component, operation string, success bool, elapsed float64) {
	if c.enabled {
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(c.meter),
			c.regRules.WithTags(c.secretCount.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, operationKey, operation, successKey, strconv.FormatBool(success)),
			stats.WithMeasurements(c.secretCount.M(1)),
		)

		if elapsed > 0 {
			stats.RecordWithOptions(
				ctx,
				stats.WithRecorder(c.meter),
				c.regRules.WithTags(c.secretLatency.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, operationKey, operation, successKey, strconv.FormatBool(success)),
				stats.WithMeasurements(c.secretLatency.M(elapsed)),
			)
		}
	}
}

// CryptoInvoked records the metrics for a crypto event.
func (c *componentMetrics) CryptoInvoked(ctx context.Context, component, operation string, success bool, elapsed float64) {
	if c.enabled {
		stats.RecordWithOptions(
			ctx,
			stats.WithRecorder(c.meter),
			c.regRules.WithTags(c.cryptoCount.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, operationKey, operation, successKey, strconv.FormatBool(success)),
			stats.WithMeasurements(c.cryptoCount.M(1)),
		)

		if elapsed > 0 {
			stats.RecordWithOptions(
				ctx,
				stats.WithRecorder(c.meter),
				c.regRules.WithTags(c.cryptoLatency.Name(), appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, operationKey, operation, successKey, strconv.FormatBool(success)),
				stats.WithMeasurements(c.cryptoLatency.M(elapsed)),
			)
		}
	}
}

func ElapsedSince(clock clock.Clock, start time.Time) float64 {
	return float64(clock.Since(start) / time.Millisecond)
}
