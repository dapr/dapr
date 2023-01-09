package diagnostics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opencensus.io/stats/view"

	"github.com/dapr/dapr/pkg/config"
)

func TestRegexRulesSingle(t *testing.T) {
	InitMetrics("testAppId2", "", []config.MetricsRule{
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

	t.Run("single regex rule applied", func(t *testing.T) {
		t.Cleanup(func() {
			view.Unregister(view.Find("runtime/service_invocation/req_sent_total"))
		})

		s := servicesMetrics()

		s.ServiceInvocationRequestSent("testAppId2", "/orders/123")

		viewData, _ := view.RetrieveData("runtime/service_invocation/req_sent_total")
		v := view.Find("runtime/service_invocation/req_sent_total")

		allTagsPresent(t, v, viewData[0].Tags)

		assert.Equal(t, "/orders/TEST", viewData[0].Tags[2].Value)
	})

	t.Run("single regex rule not applied", func(t *testing.T) {
		t.Cleanup(func() {
			view.Unregister(view.Find("runtime/service_invocation/req_sent_total"))
		})

		s := servicesMetrics()

		s.ServiceInvocationRequestSent("testAppId2", "/siths/123")

		viewData, _ := view.RetrieveData("runtime/service_invocation/req_sent_total")
		v := view.Find("runtime/service_invocation/req_sent_total")

		allTagsPresent(t, v, viewData[0].Tags)

		assert.Equal(t, "/siths/123", viewData[0].Tags[2].Value)
	})

	t.Run("correct regex rules applied", func(t *testing.T) {
		t.Cleanup(func() {
			view.Unregister(view.Find("runtime/service_invocation/req_sent_total"))
		})

		s := servicesMetrics()

		s.ServiceInvocationRequestSent("testAppId2", "/orders/123")
		s.ServiceInvocationRequestSent("testAppId2", "/lightsabers/123")

		viewData, _ := view.RetrieveData("runtime/service_invocation/req_sent_total")

		assert.Equal(t, "/orders/TEST", viewData[0].Tags[2].Value)
		assert.Equal(t, "/lightsabers/TEST", viewData[1].Tags[2].Value)
	})
}
