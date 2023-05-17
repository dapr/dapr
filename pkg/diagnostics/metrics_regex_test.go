package diagnostics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/config"
)

func TestRegexRulesSingle(t *testing.T) {
	t.Parallel()

	newMetrics := func(t *testing.T) *Metrics {
		t.Helper()
		metrics, err := NewMetrics([]config.MetricsRule{
			{
				Name: "dapr_runtime_service_invocation_req_sent_total",
				Labels: []config.MetricLabel{
					{
						Name: "method",
						Regex: map[string]string{
							"/orders/TEST":      "/orders/.+",
							"/lightsabers/TEST": "/lightsabers/.+",
						},
					},
				},
			},
		})
		require.NoError(t, err)
		require.NoError(t, metrics.Init("testAppId2", ""))
		return metrics
	}

	t.Run("single regex rule applied", func(t *testing.T) {
		t.Parallel()
		m := newMetrics(t)
		s := m.Service

		t.Cleanup(func() {
			m.Meter.Unregister(m.Meter.Find("runtime/service_invocation/req_sent_total"))
		})

		s.ServiceInvocationRequestSent(context.Background(), "testAppId2", "/orders/123")

		assert.Eventually(t, func() bool {

			viewData, err := m.Meter.RetrieveData("runtime/service_invocation/req_sent_total")
			require.NoError(t, err)
			v := m.Meter.Find("runtime/service_invocation/req_sent_total")

			allTagsPresent(t, v, viewData[0].Tags)

			return "/orders/TEST" == viewData[0].Tags[2].Value
		}, time.Second*5, time.Millisecond)
	})

	t.Run("single regex rule not applied", func(t *testing.T) {
		t.Parallel()

		m := newMetrics(t)
		s := m.Service
		t.Cleanup(func() {
			m.Meter.Unregister(m.Meter.Find("runtime/service_invocation/req_sent_total"))
		})

		s.ServiceInvocationRequestSent(context.Background(), "testAppId2", "/siths/123")

		viewData, err := m.Meter.RetrieveData("runtime/service_invocation/req_sent_total")
		require.NoError(t, err)
		v := m.Meter.Find("runtime/service_invocation/req_sent_total")

		allTagsPresent(t, v, viewData[0].Tags)

		assert.Equal(t, "/siths/123", viewData[0].Tags[2].Value)
	})

	t.Run("correct regex rules applied", func(t *testing.T) {
		t.Parallel()
		m := newMetrics(t)
		s := m.Service
		t.Cleanup(func() {
			m.Meter.Unregister(m.Meter.Find("runtime/service_invocation/req_sent_total"))
		})

		s.ServiceInvocationRequestSent(context.Background(), "testAppId2", "/orders/123")
		s.ServiceInvocationRequestSent(context.Background(), "testAppId3", "/lightsabers/123")

		viewData, err := m.Meter.RetrieveData("runtime/service_invocation/req_sent_total")
		require.NoError(t, err)

		orders := false
		lightsabers := false

		for _, v := range viewData {
			if v.Tags[1].Value == "testAppId2" && v.Tags[2].Value == "/orders/TEST" {
				orders = true
			} else if v.Tags[1].Value == "testAppId3" && v.Tags[2].Value == "/lightsabers/TEST" {
				lightsabers = true
			}
		}

		assert.True(t, orders)
		assert.True(t, lightsabers)
	})
}
