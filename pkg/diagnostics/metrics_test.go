package diagnostics

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInitMetrics(t *testing.T) {
	t.Run("init metric client with empty exporter address", func(t *testing.T) {
		client, err := InitMetrics(Injector, "", "fake-appid", "dapr-system")
		assert.Nil(t, err)
		assert.Equal(t, client.Address, defaultMetricExporterAddr)
	})
	t.Run("init metric client with empty service type", func(t *testing.T) {
		client, err := InitMetrics("unknown", defaultMetricExporterAddr, "fake-appid", "dapr-system")
		assert.NotNil(t, err)
		assert.Nil(t, client)
	})
	t.Run("init metric client with injector", func(t *testing.T) {
		client, err := InitMetrics(Injector, defaultMetricExporterAddr, "fake-appid", "dapr-system")
		assert.Nil(t, err)
		assert.NotNil(t, client)
		assert.NotNil(t, DefaultInjectorMonitoring)
		assert.Equal(t, "dapr-system", client.Namespace)
		assert.Equal(t, "fake-appid", client.AppID)
	})
	t.Run("init metric client with dapr", func(t *testing.T) {
		client, err := InitMetrics(Daprd, defaultMetricExporterAddr, "fake-appid", "dapr-system")
		assert.Nil(t, err)
		assert.NotNil(t, client)
		assert.NotNil(t, DefaultMonitoring)
		assert.NotNil(t, DefaultGRPCMonitoring)
		assert.NotNil(t, DefaultHTTPMonitoring)
		assert.NotNil(t, DefaultComponentMonitoring)
		assert.NotNil(t, DefaultResiliencyMonitoring)
	})
	t.Run("init metric client with placement", func(t *testing.T) {
		client, err := InitMetrics(Operator, defaultMetricExporterAddr, "fake-appid", "dapr-system")
		assert.Nil(t, err)
		assert.NotNil(t, client)
		assert.NotNil(t, DefaultOperatorMonitoring)
	})
	t.Run("init metric client with sentry", func(t *testing.T) {
		client, err := InitMetrics(Sentry, defaultMetricExporterAddr, "fake-appid", "dapr-system")
		assert.Nil(t, err)
		assert.NotNil(t, client)
		assert.NotNil(t, DefaultSentryMonitoring)
	})
}
