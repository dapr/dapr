package components

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	config "github.com/dapr/dapr/pkg/config/modes"
	"github.com/stretchr/testify/assert"
)

type testHandler struct {
}

func (t *testHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, r.URL.RawQuery)
}

type testHandlerComponents struct {
}

func (t *testHandlerComponents) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, `[]`)
}

func TestRequestControlPlane(t *testing.T) {
	server := httptest.NewServer(&testHandler{})
	data, err := requestControlPlane(server.URL)
	assert.NoError(t, err)
	assert.NotNil(t, data)
	server.Close()
}

func TestLoadComponents(t *testing.T) {
	server := httptest.NewServer(&testHandlerComponents{})
	configuration := config.KubernetesConfig{
		ControlPlaneAddress: server.URL,
	}

	request := &KubernetesComponents{
		config: configuration,
	}

	response, err := request.LoadComponents()
	assert.NoError(t, err)
	assert.NotNil(t, response)
	server.Close()
}
