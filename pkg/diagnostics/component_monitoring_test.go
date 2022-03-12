package diagnostics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

const (
	componentName = "test"
)

func componentsMetrics() *componentMetrics {
	c := newComponentMetrics()
	c.Init("test", "default")

	return c
}

func allTagsPresent(t *testing.T, v *view.View, tags []tag.Tag) {
	for _, k := range v.TagKeys {
		found := false

		if k.Name() == "" {
			continue
		}

		for _, tag := range tags {
			if tag.Key.Name() == "" {
				continue
			}

			if k.Name() == tag.Key.Name() {
				found = true
				break
			}
		}

		assert.True(t, found)
	}
}

func TestPubSub(t *testing.T) {
	t.Run("record ingress count", func(t *testing.T) {
		c := componentsMetrics()

		c.PubsubIngressEvent(context.Background(), componentName, "retry", "A", 0)

		viewData, _ := view.RetrieveData("component/pubsub_ingress/count")
		v := view.Find("component/pubsub_ingress/count")

		allTagsPresent(t, v, viewData[0].Tags)
	})

	t.Run("record ingress latency", func(t *testing.T) {
		c := componentsMetrics()

		c.PubsubIngressEvent(context.Background(), componentName, "retry", "A", 1)

		viewData, _ := view.RetrieveData("component/pubsub_ingress/latencies")
		v := view.Find("component/pubsub_ingress/latencies")

		allTagsPresent(t, v, viewData[0].Tags)

		assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
	})

	t.Run("record egress latency", func(t *testing.T) {
		c := componentsMetrics()

		c.PubsubEgressEvent(context.Background(), componentName, "A", true, 1)

		viewData, _ := view.RetrieveData("component/pubsub_egress/latencies")
		v := view.Find("component/pubsub_egress/latencies")

		allTagsPresent(t, v, viewData[0].Tags)

		assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
	})
}

func TestBindings(t *testing.T) {
	t.Run("record input binding count", func(t *testing.T) {
		c := componentsMetrics()

		c.InputBindingEvent(context.Background(), componentName, false, 0)

		viewData, _ := view.RetrieveData("component/input_binding/count")
		v := view.Find("component/input_binding/count")

		allTagsPresent(t, v, viewData[0].Tags)
	})

	t.Run("record input binding latency", func(t *testing.T) {
		c := componentsMetrics()

		c.InputBindingEvent(context.Background(), componentName, false, 1)

		viewData, _ := view.RetrieveData("component/input_binding/latencies")
		v := view.Find("component/input_binding/count")

		allTagsPresent(t, v, viewData[0].Tags)

		assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
	})

	t.Run("record output binding count", func(t *testing.T) {
		c := componentsMetrics()

		c.OutputBindingEvent(context.Background(), componentName, "set", false, 0)

		viewData, _ := view.RetrieveData("component/output_binding/count")
		v := view.Find("component/input_binding/count")

		allTagsPresent(t, v, viewData[0].Tags)
	})

	t.Run("record output binding latency", func(t *testing.T) {
		c := componentsMetrics()

		c.OutputBindingEvent(context.Background(), componentName, "set", false, 1)

		viewData, _ := view.RetrieveData("component/output_binding/latencies")
		v := view.Find("component/output_binding/latencies")

		allTagsPresent(t, v, viewData[0].Tags)

		assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
	})
}

func TestState(t *testing.T) {
	t.Run("record state count", func(t *testing.T) {
		c := componentsMetrics()

		c.StateInvoked(context.Background(), componentName, "get", false, 0)

		viewData, _ := view.RetrieveData("component/state/count")
		v := view.Find("component/state/count")

		allTagsPresent(t, v, viewData[0].Tags)
	})

	t.Run("record state latency", func(t *testing.T) {
		c := componentsMetrics()

		c.StateInvoked(context.Background(), componentName, "get", false, 1)

		viewData, _ := view.RetrieveData("component/state/latencies")
		v := view.Find("component/state/latencies")

		allTagsPresent(t, v, viewData[0].Tags)
		assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
	})
}

func TestConfiguration(t *testing.T) {
	t.Run("record configuration count", func(t *testing.T) {
		c := componentsMetrics()

		c.ConfigurationInvoked(context.Background(), componentName, "get", false, 0)

		viewData, _ := view.RetrieveData("component/configuration/count")
		v := view.Find("component/configuration/count")

		allTagsPresent(t, v, viewData[0].Tags)
	})

	t.Run("record configuration latency", func(t *testing.T) {
		c := componentsMetrics()

		c.ConfigurationInvoked(context.Background(), componentName, "get", false, 1)

		viewData, _ := view.RetrieveData("component/configuration/latencies")
		v := view.Find("component/configuration/latencies")

		allTagsPresent(t, v, viewData[0].Tags)

		assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
	})
}

func TestSecrets(t *testing.T) {
	t.Run("record secret count", func(t *testing.T) {
		c := componentsMetrics()

		c.SecretInvoked(context.Background(), componentName, "get", false, 0)

		viewData, _ := view.RetrieveData("component/secret/count")
		v := view.Find("component/secret/count")

		allTagsPresent(t, v, viewData[0].Tags)
	})

	t.Run("record secret latency", func(t *testing.T) {
		c := componentsMetrics()

		c.SecretInvoked(context.Background(), componentName, "get", false, 1)

		viewData, _ := view.RetrieveData("component/secret/latencies")
		v := view.Find("component/secret/latencies")

		allTagsPresent(t, v, viewData[0].Tags)

		assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
	})
}

func TestInit(t *testing.T) {
	c := componentsMetrics()
	assert.True(t, c.enabled)
	assert.Equal(t, c.appID, "test")
	assert.Equal(t, c.namespace, "default")
}

func TestElapsedSince(t *testing.T) {
	start := time.Now()
	time.Sleep(time.Second)

	elapsed := ElapsedSince(start)
	assert.True(t, elapsed >= 1000)
}
