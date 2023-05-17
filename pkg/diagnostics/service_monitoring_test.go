package diagnostics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opencensus.io/stats/view"
	"k8s.io/utils/clock"
)

func servicesMetrics(t *testing.T) (view.Meter, *serviceMetrics) {
	meter := view.NewMeter()
	s := newServiceMetrics(meter, clock.RealClock{}, nil)
	meter.Start()

	t.Cleanup(meter.Stop)

	s.init("testAppId")

	return meter, s
}

func TestServiceInvocation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)

	t.Run("record service invocation request sent", func(t *testing.T) {
		m, s := servicesMetrics(t)

		s.ServiceInvocationRequestSent(ctx, "testAppId2", "testMethod")

		viewData, err := m.RetrieveData("runtime/service_invocation/req_sent_total")
		require.NoError(t, err)
		v := m.Find("runtime/service_invocation/req_sent_total")

		allTagsPresent(t, v, viewData[0].Tags)
	})

	t.Run("record service invoation request received", func(t *testing.T) {
		m, s := servicesMetrics(t)

		s.ServiceInvocationRequestReceived(ctx, "testAppId", "testMethod")

		viewData, err := m.RetrieveData("runtime/service_invocation/req_recv_total")
		require.NoError(t, err)
		v := m.Find("runtime/service_invocation/req_recv_total")

		allTagsPresent(t, v, viewData[0].Tags)
	})

	t.Run("record service invocation response sent", func(t *testing.T) {
		m, s := servicesMetrics(t)

		s.ServiceInvocationResponseSent(ctx, "testAppId2", "testMethod", 200)

		viewData, err := m.RetrieveData("runtime/service_invocation/res_sent_total")
		require.NoError(t, err)
		v := m.Find("runtime/service_invocation/res_sent_total")

		allTagsPresent(t, v, viewData[0].Tags)
	})

	t.Run("record service invocation response received", func(t *testing.T) {
		m, s := servicesMetrics(t)

		s.ServiceInvocationResponseReceived(ctx, "testAppId", "testMethod", 200, time.Now())

		viewData, err := m.RetrieveData("runtime/service_invocation/res_recv_total")
		require.NoError(t, err)
		v := m.Find("runtime/service_invocation/res_recv_total")

		allTagsPresent(t, v, viewData[0].Tags)

		viewData2, err := m.RetrieveData("runtime/service_invocation/res_recv_latency_ms")
		require.NoError(t, err)
		v2 := m.Find("runtime/service_invocation/res_recv_latency_ms")

		allTagsPresent(t, v2, viewData2[0].Tags)
	})
}

func TestSerivceMonitoringInit(t *testing.T) {
	t.Parallel()

	_, c := servicesMetrics(t)
	assert.True(t, c.enabled)
	assert.Equal(t, c.appID, "testAppId")
}

// export for diagnostics_test package only unexported keys
var (
	FlowDirectionKey = flowDirectionKey
	TargetKey        = targetKey
	StatusKey        = statusKey
	PolicyKey        = policyKey
)
