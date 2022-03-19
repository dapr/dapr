package diagnostics

import (
	"context"
	"fmt"
	"time"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
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
)

// componentMetrics holds dapr runtime metrics for components.
type componentMetrics struct {
	pubsubIngressCount   *stats.Int64Measure
	pubsubIngressLatency *stats.Float64Measure
	pubsubEgressCount    *stats.Int64Measure
	pubsubEgressLatency  *stats.Float64Measure

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

	appID     string
	enabled   bool
	namespace string
}

// newComponentMetrics returns a componentMetrics instance with default stats.
func newComponentMetrics() *componentMetrics {
	return &componentMetrics{
		pubsubIngressCount: stats.Int64(
			"component/pubsub_ingress/count",
			"The number of incoming messages arriving from the pub/sub component.",
			stats.UnitDimensionless),
		pubsubIngressLatency: stats.Float64(
			"component/pubsub_ingress/latencies",
			"The consuming app event processing latency.",
			stats.UnitMilliseconds),
		pubsubEgressCount: stats.Int64(
			"component/pubsub_egress/count",
			"The number of outgoing messages published to the pub/sub component.",
			stats.UnitDimensionless),
		pubsubEgressLatency: stats.Float64(
			"component/pubsub_egress/latencies",
			"The latency of the response from the pub/sub component.",
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
	}
}

// Init registers the component metrics views.
func (c *componentMetrics) Init(appID, namespace string) error {
	c.appID = appID
	c.enabled = true
	c.namespace = namespace

	return view.Register(
		diag_utils.NewMeasureView(c.pubsubIngressLatency, []tag.Key{appIDKey, componentKey, namespaceKey, processStatusKey, topicKey}, defaultLatencyDistribution),
		diag_utils.NewMeasureView(c.pubsubIngressCount, []tag.Key{appIDKey, componentKey, namespaceKey, processStatusKey, topicKey}, view.Count()),
		diag_utils.NewMeasureView(c.pubsubEgressLatency, []tag.Key{appIDKey, componentKey, namespaceKey, successKey, topicKey}, defaultLatencyDistribution),
		diag_utils.NewMeasureView(c.pubsubEgressCount, []tag.Key{appIDKey, componentKey, namespaceKey, successKey, topicKey}, view.Count()),
		diag_utils.NewMeasureView(c.inputBindingLatency, []tag.Key{appIDKey, componentKey, namespaceKey, successKey}, defaultLatencyDistribution),
		diag_utils.NewMeasureView(c.inputBindingCount, []tag.Key{appIDKey, componentKey, namespaceKey, successKey}, view.Count()),
		diag_utils.NewMeasureView(c.outputBindingLatency, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, successKey}, defaultLatencyDistribution),
		diag_utils.NewMeasureView(c.outputBindingCount, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, successKey}, view.Count()),
		diag_utils.NewMeasureView(c.stateLatency, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, successKey}, defaultLatencyDistribution),
		diag_utils.NewMeasureView(c.stateCount, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, successKey}, view.Count()),
		diag_utils.NewMeasureView(c.configurationLatency, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, successKey}, defaultLatencyDistribution),
		diag_utils.NewMeasureView(c.configurationCount, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, successKey}, view.Count()),
		diag_utils.NewMeasureView(c.secretLatency, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, successKey}, defaultLatencyDistribution),
		diag_utils.NewMeasureView(c.secretCount, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, successKey}, view.Count()),
	)
}

// PubsubIngressEvent records the metrics for a pub/sub ingress event.
func (c *componentMetrics) PubsubIngressEvent(ctx context.Context, component, processStatus, topic string, elapsed float64) {
	if c.enabled {
		stats.RecordWithTags(
			ctx,
			diag_utils.WithTags(appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, processStatusKey, processStatus, topicKey, topic),
			c.pubsubIngressCount.M(1))

		if elapsed > 0 {
			stats.RecordWithTags(
				ctx,
				diag_utils.WithTags(appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, processStatusKey, processStatus, topicKey, topic),
				c.pubsubIngressLatency.M(elapsed))
		}
	}
}

// PubsubEgressEvent records the metris for a pub/sub egress event.
func (c *componentMetrics) PubsubEgressEvent(ctx context.Context, component, topic string, success bool, elapsed float64) {
	if c.enabled {
		stats.RecordWithTags(
			ctx,
			diag_utils.WithTags(appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, successKey, fmt.Sprintf("%v", success), topicKey, topic),
			c.pubsubEgressCount.M(1))

		if elapsed > 0 {
			stats.RecordWithTags(
				ctx,
				diag_utils.WithTags(appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, successKey, fmt.Sprintf("%v", success), topicKey, topic),
				c.pubsubEgressLatency.M(elapsed))
		}
	}
}

// InputBindingEvent records the metrics for an input binding event.
func (c *componentMetrics) InputBindingEvent(ctx context.Context, component string, success bool, elapsed float64) {
	if c.enabled {
		stats.RecordWithTags(
			ctx,
			diag_utils.WithTags(appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, successKey, fmt.Sprintf("%v", success)),
			c.inputBindingCount.M(1))

		if elapsed > 0 {
			stats.RecordWithTags(
				ctx,
				diag_utils.WithTags(appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, successKey, fmt.Sprintf("%v", success)),
				c.inputBindingLatency.M(elapsed))
		}
	}
}

// OutputBindingEvent records the metrics for an output binding event.
func (c *componentMetrics) OutputBindingEvent(ctx context.Context, component, operation string, success bool, elapsed float64) {
	if c.enabled {
		stats.RecordWithTags(
			ctx,
			diag_utils.WithTags(appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, operationKey, operation, successKey, fmt.Sprintf("%v", success)),
			c.outputBindingCount.M(1))

		if elapsed > 0 {
			stats.RecordWithTags(
				ctx,
				diag_utils.WithTags(appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, operationKey, operation, successKey, fmt.Sprintf("%v", success)),
				c.outputBindingLatency.M(elapsed))
		}
	}
}

// StateInvoked records the metrics for a state event.
func (c *componentMetrics) StateInvoked(ctx context.Context, component, operation string, success bool, elapsed float64) {
	if c.enabled {
		stats.RecordWithTags(
			ctx,
			diag_utils.WithTags(appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, operationKey, operation, successKey, fmt.Sprintf("%v", success)),
			c.stateCount.M(1))

		if elapsed > 0 {
			stats.RecordWithTags(
				ctx,
				diag_utils.WithTags(appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, operationKey, operation, successKey, fmt.Sprintf("%v", success)),
				c.stateLatency.M(elapsed))
		}
	}
}

// ConfigurationInvoked records the metrics for a configuration event.
func (c *componentMetrics) ConfigurationInvoked(ctx context.Context, component, operation string, success bool, elapsed float64) {
	if c.enabled {
		stats.RecordWithTags(
			ctx,
			diag_utils.WithTags(appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, operationKey, operation, successKey, fmt.Sprintf("%v", success)),
			c.configurationCount.M(1))

		if elapsed > 0 {
			stats.RecordWithTags(
				ctx,
				diag_utils.WithTags(appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, operationKey, operation, successKey, fmt.Sprintf("%v", success)),
				c.configurationLatency.M(elapsed))
		}
	}
}

// SecretInvoked records the metrics for a secret event.
func (c *componentMetrics) SecretInvoked(ctx context.Context, component, operation string, success bool, elapsed float64) {
	if c.enabled {
		stats.RecordWithTags(
			ctx,
			diag_utils.WithTags(appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, operationKey, operation, successKey, fmt.Sprintf("%v", success)),
			c.secretCount.M(1))

		if elapsed > 0 {
			stats.RecordWithTags(
				ctx,
				diag_utils.WithTags(appIDKey, c.appID, componentKey, component, namespaceKey, c.namespace, operationKey, operation, successKey, fmt.Sprintf("%v", success)),
				c.secretLatency.M(elapsed))
		}
	}
}

func ElapsedSince(start time.Time) float64 {
	return float64(time.Since(start) / time.Millisecond)
}
