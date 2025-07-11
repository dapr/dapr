package diagnostics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opencensus.io/stats/view"

	"github.com/dapr/dapr/pkg/config"
)

func servicesMetrics() (*serviceMetrics, view.Meter) {
	s := newServiceMetrics()
	meter := view.NewMeter()
	meter.Start()
	_ = s.Init(meter, "testAppId", config.LoadDefaultConfiguration().GetMetricsSpec().GetLatencyDistribution(log))

	return s, meter
}

func TestServiceInvocation(t *testing.T) {
	t.Run("record service invocation request sent", func(t *testing.T) {
		s, meter := servicesMetrics()
		t.Cleanup(func() { meter.Stop() })

		s.ServiceInvocationRequestSent("testAppId2")

		viewData, _ := meter.RetrieveData("runtime/service_invocation/req_sent_total")
		v := meter.Find("runtime/service_invocation/req_sent_total")

		allTagsPresent(t, v, viewData[0].Tags)
		RequireTagExist(t, viewData, NewTag(typeKey.Name(), typeUnary))
	})

	t.Run("record service invocation streaming request sent", func(t *testing.T) {
		s, meter := servicesMetrics()
		t.Cleanup(func() { meter.Stop() })

		s.ServiceInvocationStreamingRequestSent("testAppId2")

		viewData, _ := meter.RetrieveData("runtime/service_invocation/req_sent_total")
		v := meter.Find("runtime/service_invocation/req_sent_total")

		allTagsPresent(t, v, viewData[0].Tags)
		RequireTagExist(t, viewData, NewTag(typeKey.Name(), typeStreaming))
	})

	t.Run("record service invocation request received", func(t *testing.T) {
		s, meter := servicesMetrics()
		t.Cleanup(func() { meter.Stop() })

		s.ServiceInvocationRequestReceived("testAppId")

		viewData, _ := meter.RetrieveData("runtime/service_invocation/req_recv_total")
		v := meter.Find("runtime/service_invocation/req_recv_total")

		allTagsPresent(t, v, viewData[0].Tags)
	})

	t.Run("record service invocation response sent", func(t *testing.T) {
		s, meter := servicesMetrics()
		t.Cleanup(func() { meter.Stop() })

		s.ServiceInvocationResponseSent("testAppId2", 200)

		viewData, _ := meter.RetrieveData("runtime/service_invocation/res_sent_total")
		v := meter.Find("runtime/service_invocation/res_sent_total")

		allTagsPresent(t, v, viewData[0].Tags)
	})

	t.Run("record service invocation response received", func(t *testing.T) {
		s, meter := servicesMetrics()
		t.Cleanup(func() { meter.Stop() })

		s.ServiceInvocationResponseReceived("testAppId", 200, time.Now())

		viewData, _ := meter.RetrieveData("runtime/service_invocation/res_recv_total")
		v := meter.Find("runtime/service_invocation/res_recv_total")

		allTagsPresent(t, v, viewData[0].Tags)

		viewData2, _ := meter.RetrieveData("runtime/service_invocation/res_recv_latency_ms")
		v2 := meter.Find("runtime/service_invocation/res_recv_latency_ms")

		allTagsPresent(t, v2, viewData2[0].Tags)
	})
}

func TestSerivceMonitoringInit(t *testing.T) {
	c, meter := servicesMetrics()
	t.Cleanup(func() {
		meter.Stop()
	})
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
