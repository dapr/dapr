package diagnostics

import (
	"sync"
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

// Security starts before runtime.FromConfig initializes the metrics meter.
func TestMTLSMetricsBeforeInit(t *testing.T) {
	s := newServiceMetrics()
	record := func() {
		s.MTLSInitCompleted()
		s.MTLSInitFailed("sign")
		s.MTLSWorkLoadCertRotationCompleted()
		s.MTLSWorkLoadCertRotationFailed("sentry_token")
	}
	record()
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(meter.Stop)
	require.NoError(t, s.Init(meter, "sidecar", view.Distribution(1)))
	check := func(want int64) {
		for _, tc := range []struct{ name, reason string }{
			{"init_total", ""},
			{"init_fail_total", "sign"},
			{"workload_cert_rotated_total", ""},
			{"workload_cert_rotated_fail_total", "sentry_token"},
		} {
			rows, err := meter.RetrieveData("runtime/mtls/" + tc.name)
			require.NoError(t, err)
			require.Len(t, rows, 1, "startup observation missing: %s", tc.name)
			assert.Equal(t, want, rows[0].Data.(*view.CountData).Value, tc.name)
			RequireTagExist(t, rows, NewTag("app_id", "sidecar"))
			if tc.reason != "" {
				RequireTagExist(t, rows, NewTag("reason", tc.reason))
			}
		}
	}
	check(1)
	record()
	check(2)
	// Reinitialization must not replay observations that have already been flushed.
	second := view.NewMeter()
	second.Start()
	t.Cleanup(second.Stop)
	require.NoError(t, s.Init(second, "second", view.Distribution(1)))
	rows, err := second.RetrieveData("runtime/mtls/init_total")
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestMTLSMetricsConcurrentInit(t *testing.T) {
	s := newServiceMetrics()
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(meter.Stop)
	started := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		s.MTLSInitCompleted()
		close(started)
		for range 99 {
			s.MTLSInitCompleted()
		}
	})
	<-started
	require.NoError(t, s.Init(meter, "sidecar", view.Distribution(1)))
	wg.Wait()
	rows, err := meter.RetrieveData("runtime/mtls/init_total")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(100), rows[0].Data.(*view.CountData).Value)
}

// NewServiceMetricsForTest gives external integration tests isolated monitoring state.
func NewServiceMetricsForTest() *serviceMetrics {
	return newServiceMetrics()
}
