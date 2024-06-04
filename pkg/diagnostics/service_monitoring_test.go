package diagnostics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opencensus.io/stats/view"
)

func servicesMetrics() *serviceMetrics {
	s := newServiceMetrics()
	s.Init("testAppId")

	return s
}

func TestServiceInvocation(t *testing.T) {
	t.Run("record service invocation request sent", func(t *testing.T) {
		s := servicesMetrics()

		s.ServiceInvocationRequestSent("testAppId2")

		viewData, _ := view.RetrieveData("runtime/service_invocation/req_sent_total")
		v := view.Find("runtime/service_invocation/req_sent_total")

		allTagsPresent(t, v, viewData[0].Tags)
		RequireTagExist(t, viewData, NewTag(typeKey.Name(), typeUnary))
	})

	t.Run("record service invocation streaming request sent", func(t *testing.T) {
		s := servicesMetrics()

		s.ServiceInvocationStreamingRequestSent("testAppId2")

		viewData, _ := view.RetrieveData("runtime/service_invocation/req_sent_total")
		v := view.Find("runtime/service_invocation/req_sent_total")

		allTagsPresent(t, v, viewData[0].Tags)
		RequireTagExist(t, viewData, NewTag(typeKey.Name(), typeStreaming))
	})

	t.Run("record service invocation request received", func(t *testing.T) {
		s := servicesMetrics()

		s.ServiceInvocationRequestReceived("testAppId")

		viewData, _ := view.RetrieveData("runtime/service_invocation/req_recv_total")
		v := view.Find("runtime/service_invocation/req_recv_total")

		allTagsPresent(t, v, viewData[0].Tags)
	})

	t.Run("record service invocation response sent", func(t *testing.T) {
		s := servicesMetrics()

		s.ServiceInvocationResponseSent("testAppId2", 200)

		viewData, _ := view.RetrieveData("runtime/service_invocation/res_sent_total")
		v := view.Find("runtime/service_invocation/res_sent_total")

		allTagsPresent(t, v, viewData[0].Tags)
	})

	t.Run("record service invocation response received", func(t *testing.T) {
		s := servicesMetrics()

		s.ServiceInvocationResponseReceived("testAppId", 200, time.Now())

		viewData, _ := view.RetrieveData("runtime/service_invocation/res_recv_total")
		v := view.Find("runtime/service_invocation/res_recv_total")

		allTagsPresent(t, v, viewData[0].Tags)

		viewData2, _ := view.RetrieveData("runtime/service_invocation/res_recv_latency_ms")
		v2 := view.Find("runtime/service_invocation/res_recv_latency_ms")

		allTagsPresent(t, v2, viewData2[0].Tags)
	})
}

func TestSerivceMonitoringInit(t *testing.T) {
	c := servicesMetrics()
	assert.True(t, c.enabled)
	assert.Equal(t, "testAppId", c.appID)
}

// export for diagnostics_test package only unexported keys
var (
	FlowDirectionKey = flowDirectionKey
	TargetKey        = targetKey
	StatusKey        = statusKey
	PolicyKey        = policyKey
)
