package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(errors))
}

type errors struct {
	daprd1 *procdaprd.Daprd
	daprd2 *procdaprd.Daprd
}

func (e *errors) Setup(t *testing.T) []framework.Option {
	newHTTPServer := func() *prochttp.HTTP {
		handler := http.NewServeMux()
		return prochttp.New(t, prochttp.WithHandler(handler))
	}
	srv1 := newHTTPServer()
	srv2 := newHTTPServer()
	e.daprd1 = procdaprd.New(t, procdaprd.WithAppPort(srv1.Port()))
	e.daprd2 = procdaprd.New(t, procdaprd.WithAppPort(srv2.Port()))

	return []framework.Option{
		framework.WithProcesses(srv1, srv2, e.daprd1, e.daprd2),
	}
}

func (e *errors) Run(t *testing.T, ctx context.Context) {
	e.daprd1.WaitUntilRunning(t, ctx)
	e.daprd2.WaitUntilRunning(t, ctx)

	// Covers apierrors.NoAppId()
	t.Run("no app id", func(t *testing.T) {
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/doesntexist", e.daprd1.HTTPPort(), "")
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
		require.NoError(t, err)
		resp, err := util.HTTPClient(t).Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		require.Equal(t, "ERR_DIRECT_INVOKE", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, "failed getting app id either from the URL path or the header dapr-app-id", errMsg)

		// Confirm that the 'details' field exists and has one element
		details, exists := data["details"]
		require.True(t, exists)

		detailsArray, ok := details.([]interface{})
		require.True(t, ok)
		require.Len(t, detailsArray, 2)

		// Confirm that the first element of the 'details' array has the correct ErrorInfo details
		// wip

	})
}
