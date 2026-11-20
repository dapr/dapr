package diagnostics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestMTLS(t *testing.T) {
	t.Run("record mtls init completed", func(t *testing.T) {
		s, meter := servicesMetrics()
		t.Cleanup(func() { meter.Stop() })

		s.MTLSInitCompleted()

		viewData, _ := meter.RetrieveData("runtime/mtls/init_total")
		v := meter.Find("runtime/mtls/init_total")

		require.Len(t, viewData, 1)
		allTagsPresent(t, v, viewData[0].Tags)
		assert.Equal(t, int64(1), viewData[0].Data.(*view.CountData).Value)
	})

	t.Run("record mtls init failed", func(t *testing.T) {
		s, meter := servicesMetrics()
		t.Cleanup(func() { meter.Stop() })

		s.MTLSInitFailed("sign")

		viewData, _ := meter.RetrieveData("runtime/mtls/init_fail_total")
		v := meter.Find("runtime/mtls/init_fail_total")

		require.Len(t, viewData, 1)
		allTagsPresent(t, v, viewData[0].Tags)
		RequireTagExist(t, viewData, NewTag(failReasonKey.Name(), "sign"))
	})

	t.Run("record workload cert rotation completed", func(t *testing.T) {
		s, meter := servicesMetrics()
		t.Cleanup(func() { meter.Stop() })

		s.MTLSWorkLoadCertRotationCompleted()
		s.MTLSWorkLoadCertRotationCompleted()

		viewData, _ := meter.RetrieveData("runtime/mtls/workload_cert_rotated_total")
		v := meter.Find("runtime/mtls/workload_cert_rotated_total")

		require.Len(t, viewData, 1)
		allTagsPresent(t, v, viewData[0].Tags)
		assert.Equal(t, int64(2), viewData[0].Data.(*view.CountData).Value)
	})

	t.Run("record workload cert rotation failed", func(t *testing.T) {
		s, meter := servicesMetrics()
		t.Cleanup(func() { meter.Stop() })

		s.MTLSWorkLoadCertRotationFailed("sentry_conn")

		viewData, _ := meter.RetrieveData("runtime/mtls/workload_cert_rotated_fail_total")
		v := meter.Find("runtime/mtls/workload_cert_rotated_fail_total")

		require.Len(t, viewData, 1)
		allTagsPresent(t, v, viewData[0].Tags)
		RequireTagExist(t, viewData, NewTag(failReasonKey.Name(), "sentry_conn"))
	})

	t.Run("mtls init completed before init is not recorded", func(t *testing.T) {
		s := newServiceMetrics()
		s.MTLSInitCompleted()

		meter := view.NewMeter()
		meter.Start()
		t.Cleanup(func() { meter.Stop() })
		require.NoError(t, s.Init(meter, "testAppId", config.LoadDefaultConfiguration().GetMetricsSpec().GetLatencyDistribution(log)))

		viewData, _ := meter.RetrieveData("runtime/mtls/init_total")
		assert.Empty(t, viewData)
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
