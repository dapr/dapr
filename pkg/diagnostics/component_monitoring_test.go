package diagnostics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opencensus.io/stats/view"
)

const (
	componentName  = "test"
	componentLabel = "component"
)

func componentsMetrics() *componentMetrics {
	c := newComponentMetrics()
	c.Init("test", "default")

	return c
}

func TestPubSub(t *testing.T) {
	t.Run("record ingress count", func(t *testing.T) {
		c := componentsMetrics()

		c.PubsubIngressEvent(context.Background(), componentName, "", "", 0)

		views, _ := view.RetrieveData("component/pubsub_ingress/event_count")

		recorded := false
		for _, t := range views[0].Tags {
			if t.Key.Name() == componentLabel && t.Value == componentName {
				recorded = true
			}
		}

		assert.True(t, recorded)
	})

	t.Run("record ingress latency", func(t *testing.T) {
		c := componentsMetrics()

		c.PubsubIngressEvent(context.Background(), componentName, "", "", 1)

		views, _ := view.RetrieveData("component/pubsub_ingress/event_latencies")

		recorded := false
		for _, t := range views[0].Tags {
			if t.Key.Name() == componentLabel && t.Value == componentName {
				recorded = true
			}
		}

		assert.Equal(t, float64(1), views[0].Data.(*view.DistributionData).Min)
		assert.True(t, recorded)
	})

	t.Run("record egress count", func(t *testing.T) {
		c := componentsMetrics()

		c.PubsubEgressEvent(context.Background(), componentName, "", false, 0)

		views, _ := view.RetrieveData("component/pubsub_egress/event_count")

		recorded := false
		for _, t := range views[0].Tags {
			if t.Key.Name() == componentLabel && t.Value == componentName {
				recorded = true
			}
		}

		assert.True(t, recorded)
	})

	t.Run("record egress latency", func(t *testing.T) {
		c := componentsMetrics()

		c.PubsubEgressEvent(context.Background(), componentName, "", false, 1)

		views, _ := view.RetrieveData("component/pubsub_egress/event_latencies")

		recorded := false
		for _, t := range views[0].Tags {
			if t.Key.Name() == componentLabel && t.Value == componentName {
				recorded = true
			}
		}

		assert.Equal(t, float64(1), views[0].Data.(*view.DistributionData).Min)
		assert.True(t, recorded)
	})
}

func TestBindings(t *testing.T) {
	t.Run("record input binding count", func(t *testing.T) {
		c := componentsMetrics()

		c.InputBindingEvent(context.Background(), componentName, false, 0)

		views, _ := view.RetrieveData("component/input_binding/event_count")

		recorded := false
		for _, t := range views[0].Tags {
			if t.Key.Name() == componentLabel && t.Value == componentName {
				recorded = true
			}
		}

		assert.True(t, recorded)
	})

	t.Run("record input binding latency", func(t *testing.T) {
		c := componentsMetrics()

		c.InputBindingEvent(context.Background(), componentName, false, 1)

		views, _ := view.RetrieveData("component/input_binding/event_latencies")

		recorded := false
		for _, t := range views[0].Tags {
			if t.Key.Name() == componentLabel && t.Value == componentName {
				recorded = true
			}
		}

		assert.Equal(t, float64(1), views[0].Data.(*view.DistributionData).Min)
		assert.True(t, recorded)
	})

	t.Run("record output binding count", func(t *testing.T) {
		c := componentsMetrics()

		c.OutputBindingEvent(context.Background(), componentName, "", false, 0)

		views, _ := view.RetrieveData("component/output_binding/event_count")

		recorded := false
		for _, t := range views[0].Tags {
			if t.Key.Name() == componentLabel && t.Value == componentName {
				recorded = true
			}
		}

		assert.True(t, recorded)
	})

	t.Run("record output binding latency", func(t *testing.T) {
		c := componentsMetrics()

		c.OutputBindingEvent(context.Background(), componentName, "", false, 1)

		views, _ := view.RetrieveData("component/output_binding/event_latencies")

		recorded := false
		for _, t := range views[0].Tags {
			if t.Key.Name() == componentLabel && t.Value == componentName {
				recorded = true
			}
		}

		assert.Equal(t, float64(1), views[0].Data.(*view.DistributionData).Min)
		assert.True(t, recorded)
	})
}

func TestState(t *testing.T) {
	t.Run("record state count", func(t *testing.T) {
		c := componentsMetrics()

		c.StateInvoked(context.Background(), componentName, "get", false, 0)

		views, _ := view.RetrieveData("component/state/state_count")

		recorded := false
		for _, t := range views[0].Tags {
			if t.Key.Name() == componentLabel && t.Value == componentName {
				recorded = true
			}
		}

		assert.True(t, recorded)
	})

	t.Run("record state latency", func(t *testing.T) {
		c := componentsMetrics()

		c.StateInvoked(context.Background(), componentName, "get", false, 1)

		views, _ := view.RetrieveData("component/state/state_latencies")

		recorded := false
		for _, t := range views[0].Tags {
			if t.Key.Name() == componentLabel && t.Value == componentName {
				recorded = true
			}
		}

		assert.Equal(t, float64(1), views[0].Data.(*view.DistributionData).Min)
		assert.True(t, recorded)
	})
}

func TestConfiguration(t *testing.T) {
	t.Run("record configuration count", func(t *testing.T) {
		c := componentsMetrics()

		c.ConfigurationInvoked(context.Background(), componentName, "get", false, 0)

		views, _ := view.RetrieveData("component/configuration/configuration_count")

		recorded := false
		for _, t := range views[0].Tags {
			if t.Key.Name() == componentLabel && t.Value == componentName {
				recorded = true
			}
		}

		assert.True(t, recorded)
	})

	t.Run("record configuration latency", func(t *testing.T) {
		c := componentsMetrics()

		c.ConfigurationInvoked(context.Background(), componentName, "get", false, 1)

		views, _ := view.RetrieveData("component/configuration/configuration_latencies")

		recorded := false
		for _, t := range views[0].Tags {
			if t.Key.Name() == componentLabel && t.Value == componentName {
				recorded = true
			}
		}

		assert.Equal(t, float64(1), views[0].Data.(*view.DistributionData).Min)
		assert.True(t, recorded)
	})
}

func TestSecrets(t *testing.T) {
	t.Run("record secret count", func(t *testing.T) {
		c := componentsMetrics()

		c.SecretInvoked(context.Background(), componentName, "get", false, 0)

		views, _ := view.RetrieveData("component/secret/secret_count")

		recorded := false
		for _, t := range views[0].Tags {
			if t.Key.Name() == componentLabel && t.Value == componentName {
				recorded = true
			}
		}

		assert.True(t, recorded)
	})

	t.Run("record secret latency", func(t *testing.T) {
		c := componentsMetrics()

		c.SecretInvoked(context.Background(), componentName, "get", false, 1)

		views, _ := view.RetrieveData("component/secret/secret_latencies")

		recorded := false
		for _, t := range views[0].Tags {
			if t.Key.Name() == componentLabel && t.Value == componentName {
				recorded = true
			}
		}

		assert.Equal(t, float64(1), views[0].Data.(*view.DistributionData).Min)
		assert.True(t, recorded)
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
