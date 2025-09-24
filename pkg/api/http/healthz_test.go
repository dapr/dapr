package http

import (
	"net/http"
	"testing"

	"github.com/dapr/dapr/pkg/api/universal"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/messages/errorcodes"
	"github.com/stretchr/testify/assert"
)

func TestV1HealthzEndpoint(t *testing.T) {
	fakeServer := newFakeHTTPServer()

	const appID = "fakeAPI"
	healthz := healthz.New()
	htarget := healthz.AddTarget("test-target")
	testAPI := &api{
		healthz: healthz,
		universal: universal.New(universal.Options{
			AppID: appID,
		}),
	}

	fakeServer.StartServer(testAPI.constructHealthzEndpoints(), nil)
	defer fakeServer.Shutdown()

	apiPath := apiVersionV1 + "/healthz"

	t.Run("Healthz - 500 ERR_HEALTH_NOT_READY", func(t *testing.T) {
		resp := fakeServer.DoRequest(http.MethodGet, apiPath, nil, nil)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode, "dapr not ready should return 500")

		expErrBody := map[string]string{"details": "", "errorCode": errorcodes.HealthNotReady.Code, "message": "dapr is not ready: [test-target]"}
		assert.Equal(t, expErrBody, resp.ErrorBody)
	})

	t.Run("Healthz - 500 ERR_HEALTH_APPID_NOT_MATCH", func(t *testing.T) {
		htarget.Ready()
		t.Cleanup(htarget.NotReady)

		resp := fakeServer.DoRequest(http.MethodGet, apiPath, nil, map[string]string{"appid": "not-test"})
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		expErrBody := map[string]string{"details": "", "errorCode": errorcodes.HealthAppidNotMatch.Code, "message": "dapr app-id does not match"}
		assert.Equal(t, expErrBody, resp.ErrorBody)
	})

	t.Run("Healthz - 204 No Content", func(t *testing.T) {
		htarget.Ready()
		t.Cleanup(htarget.NotReady)

		resp := fakeServer.DoRequest(http.MethodGet, apiPath, nil, nil)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})

	t.Run("Healthz - 204 AppId Match", func(t *testing.T) {
		htarget.Ready()
		t.Cleanup(htarget.NotReady)

		resp := fakeServer.DoRequest(http.MethodGet, apiPath, nil, map[string]string{"appid": appID})
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})

}

func TestV1HealthzOutboundEndpoint(t *testing.T) {
	fakeServer := newFakeHTTPServer()

	const appID = "fakeAPI"
	healthz := healthz.New()
	htarget := healthz.AddTarget("test-target")

	testAPI := &api{
		universal: universal.New(universal.Options{
			AppID: appID,
		}),
		outboundHealthz: healthz,
	}

	fakeServer.StartServer(testAPI.constructHealthzEndpoints(), nil)
	defer fakeServer.Shutdown()

	apiPath := apiVersionV1 + "/healthz/outbound"

	t.Run("Healthz - 500 ERR_OUTBOUND_HEALTH_NOT_READY", func(t *testing.T) {
		resp := fakeServer.DoRequest(http.MethodGet, apiPath, nil, nil)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode, "dapr not ready should return 500")

		expErrBody := map[string]string{"details": "", "errorCode": errorcodes.HealthOutboundNotReady.Code, "message": "dapr outbound is not ready"}
		assert.Equal(t, expErrBody, resp.ErrorBody)
	})

	t.Run("Healthz - 204 No Content", func(t *testing.T) {
		htarget.Ready()
		t.Cleanup(htarget.NotReady)

		resp := fakeServer.DoRequest(http.MethodGet, apiPath, nil, nil)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})
}
