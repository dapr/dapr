package diagnostics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opencensus.io/stats/view"
	clocktesting "k8s.io/utils/clock/testing"
)

const (
	componentName = "test"
)

func componentsMetrics(t *testing.T) (view.Meter, *componentMetrics) {
	meter := view.NewMeter()
	c := newComponentMetrics(meter, nil)

	meter.Start()

	t.Cleanup(meter.Stop)

	c.init("test", "default")

	return meter, c
}

func TestPubSub(t *testing.T) {
	t.Parallel()

	t.Run("record ingress count", func(t *testing.T) {
		t.Parallel()
		meter, c := componentsMetrics(t)

		c.PubsubIngressEvent(context.Background(), componentName, "retry", "A", 0)

		viewData, err := meter.RetrieveData("component/pubsub_ingress/count")
		require.NoError(t, err)
		v := meter.Find("component/pubsub_ingress/count")

		allTagsPresent(t, v, viewData[0].Tags)
	})

	t.Run("record ingress latency", func(t *testing.T) {
		t.Parallel()
		meter, c := componentsMetrics(t)

		c.PubsubIngressEvent(context.Background(), componentName, "retry", "A", 1)

		viewData, err := meter.RetrieveData("component/pubsub_ingress/latencies")
		require.NoError(t, err)
		v := meter.Find("component/pubsub_ingress/latencies")

		allTagsPresent(t, v, viewData[0].Tags)

		assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
	})

	t.Run("record egress latency", func(t *testing.T) {
		t.Parallel()
		meter, c := componentsMetrics(t)

		c.PubsubEgressEvent(context.Background(), componentName, "A", true, 1)

		viewData, err := meter.RetrieveData("component/pubsub_egress/latencies")
		require.NoError(t, err)
		v := meter.Find("component/pubsub_egress/latencies")

		allTagsPresent(t, v, viewData[0].Tags)

		assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
	})
}

func TestBindings(t *testing.T) {
	t.Parallel()

	t.Run("record input binding count", func(t *testing.T) {
		t.Parallel()
		meter, c := componentsMetrics(t)

		c.InputBindingEvent(context.Background(), componentName, false, 0)

		viewData, err := meter.RetrieveData("component/input_binding/count")
		require.NoError(t, err)
		v := meter.Find("component/input_binding/count")

		allTagsPresent(t, v, viewData[0].Tags)
	})

	t.Run("record input binding latency", func(t *testing.T) {
		meter, c := componentsMetrics(t)

		c.InputBindingEvent(context.Background(), componentName, false, 1)

		viewData, err := meter.RetrieveData("component/input_binding/latencies")
		require.NoError(t, err)
		v := meter.Find("component/input_binding/count")

		allTagsPresent(t, v, viewData[0].Tags)

		assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
	})

	t.Run("record output binding count", func(t *testing.T) {
		t.Parallel()
		meter, c := componentsMetrics(t)

		c.OutputBindingEvent(context.Background(), componentName, "set", false, 0)

		viewData, err := meter.RetrieveData("component/output_binding/count")
		require.NoError(t, err)
		v := meter.Find("component/input_binding/count")

		allTagsPresent(t, v, viewData[0].Tags)
	})

	t.Run("record output binding latency", func(t *testing.T) {
		meter, c := componentsMetrics(t)

		c.OutputBindingEvent(context.Background(), componentName, "set", false, 1)

		viewData, err := meter.RetrieveData("component/output_binding/latencies")
		require.NoError(t, err)
		v := meter.Find("component/output_binding/latencies")

		allTagsPresent(t, v, viewData[0].Tags)

		assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
	})
}

func TestState(t *testing.T) {
	t.Parallel()

	t.Run("record state count", func(t *testing.T) {
		t.Parallel()
		meter, c := componentsMetrics(t)

		c.StateInvoked(context.Background(), componentName, "get", false, 0)

		viewData, err := meter.RetrieveData("component/state/count")
		require.NoError(t, err)
		v := meter.Find("component/state/count")

		allTagsPresent(t, v, viewData[0].Tags)
	})

	t.Run("record state latency", func(t *testing.T) {
		t.Parallel()
		meter, c := componentsMetrics(t)

		c.StateInvoked(context.Background(), componentName, "get", false, 1)

		viewData, err := meter.RetrieveData("component/state/latencies")
		require.NoError(t, err)
		v := meter.Find("component/state/latencies")

		allTagsPresent(t, v, viewData[0].Tags)
		assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
	})
}

func TestConfiguration(t *testing.T) {
	t.Parallel()

	t.Run("record configuration count", func(t *testing.T) {
		t.Parallel()
		meter, c := componentsMetrics(t)

		c.ConfigurationInvoked(context.Background(), componentName, "get", false, 0)

		viewData, err := meter.RetrieveData("component/configuration/count")
		require.NoError(t, err)
		v := meter.Find("component/configuration/count")

		allTagsPresent(t, v, viewData[0].Tags)
	})

	t.Run("record configuration latency", func(t *testing.T) {
		t.Parallel()
		meter, c := componentsMetrics(t)

		c.ConfigurationInvoked(context.Background(), componentName, "get", false, 1)

		viewData, err := meter.RetrieveData("component/configuration/latencies")
		require.NoError(t, err)
		v := meter.Find("component/configuration/latencies")

		allTagsPresent(t, v, viewData[0].Tags)

		assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
	})
}

func TestSecrets(t *testing.T) {
	t.Parallel()

	t.Run("record secret count", func(t *testing.T) {
		t.Parallel()
		meter, c := componentsMetrics(t)

		c.SecretInvoked(context.Background(), componentName, "get", false, 0)

		viewData, err := meter.RetrieveData("component/secret/count")
		require.NoError(t, err)
		v := meter.Find("component/secret/count")

		allTagsPresent(t, v, viewData[0].Tags)
	})

	t.Run("record secret latency", func(t *testing.T) {
		t.Parallel()
		meter, c := componentsMetrics(t)

		c.SecretInvoked(context.Background(), componentName, "get", false, 1)

		viewData, err := meter.RetrieveData("component/secret/latencies")
		require.NoError(t, err)
		v := meter.Find("component/secret/latencies")

		allTagsPresent(t, v, viewData[0].Tags)

		assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
	})
}

func TestComponentMetricsInit(t *testing.T) {
	t.Parallel()
	_, c := componentsMetrics(t)
	assert.True(t, c.enabled)
	assert.Equal(t, c.appID, "test")
	assert.Equal(t, c.namespace, "default")
}

func TestElapsedSince(t *testing.T) {
	t.Parallel()
	clock := clocktesting.NewFakeClock(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	start := clock.Now()
	clock.Step(time.Second)

	elapsed := ElapsedSince(clock, start)
	assert.LessOrEqual(t, elapsed, float64(time.Second))
}
