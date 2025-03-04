/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

const (
	ErrInfoType = "type.googleapis.com/google.rpc.ErrorInfo"
)

func init() {
	suite.Register(new(errors))
}

type errors struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
}

func (e *errors) Setup(t *testing.T) []framework.Option {
	e.scheduler = scheduler.New(t)

	e.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(e.scheduler.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(e.scheduler, e.daprd),
	}
}

func (e *errors) Run(t *testing.T, ctx context.Context) {
	e.scheduler.WaitUntilRunning(t, ctx)
	e.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	// Covers apierrors.Empty() job name is empty
	t.Run("schedule is empty", func(t *testing.T) {
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/jobs/test", e.daprd.HTTPPort())
		payload := `{}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)

		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		assert.Equal(t, "DAPR_SCHEDULER_SCHEDULE_EMPTY", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, "Schedule is empty", errMsg)

		// Confirm that the 'details' field exists and has one element
		details, exists := data["details"]
		require.True(t, exists)

		detailsArray, ok := details.([]interface{})
		require.True(t, ok)
		require.Len(t, detailsArray, 1)

		// Confirm that the first element of the 'details' array has the correct ErrorInfo details
		detailsObject, ok := detailsArray[0].(map[string]interface{})
		require.True(t, ok)
		require.Equal(t, "dapr.io", detailsObject["domain"])
		assert.Equal(t, "DAPR_SCHEDULER_SCHEDULE_EMPTY", detailsObject["reason"])

		require.Equal(t, ErrInfoType, detailsObject["@type"])
	})

	// Covers apierrors.Empty() job schedule is empty
	t.Run("schedule job name is empty", func(t *testing.T) {
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/jobs/ ", e.daprd.HTTPPort())
		payload := `{"schedule": "@daily"}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)

		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		assert.Equal(t, "DAPR_SCHEDULER_JOB_NAME_EMPTY", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, "Name is empty", errMsg)

		// Confirm that the 'details' field exists and has one element
		details, exists := data["details"]
		require.True(t, exists)

		detailsArray, ok := details.([]interface{})
		require.True(t, ok)
		require.Len(t, detailsArray, 1)

		// Confirm that the first element of the 'details' array has the correct ErrorInfo details
		detailsObject, ok := detailsArray[0].(map[string]interface{})
		require.True(t, ok)
		require.Equal(t, "dapr.io", detailsObject["domain"])
		assert.Equal(t, "DAPR_SCHEDULER_JOB_NAME_EMPTY", detailsObject["reason"])
		require.Equal(t, ErrInfoType, detailsObject["@type"])
	})

	// Covers apierrors.SchedulerURLName() where a user specifies the job name in the url and body
	t.Run("schedule two job names", func(t *testing.T) {
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/jobs/test", e.daprd.HTTPPort())
		payload := `{"name": "test1", "schedule": "test", "repeats": 1}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)

		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		assert.Equal(t, "DAPR_SCHEDULER_JOB_NAME", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		require.Equal(t, "Set the job name in the url only", errMsg)

		// Confirm that the 'details' field exists and has one element
		details, exists := data["details"]
		require.True(t, exists)

		detailsArray, ok := details.([]interface{})
		require.True(t, ok)
		require.Len(t, detailsArray, 1)

		// Confirm that the first element of the 'details' array has the correct ErrorInfo details
		detailsObject, ok := detailsArray[0].(map[string]interface{})
		require.True(t, ok)
		require.Equal(t, "dapr.io", detailsObject["domain"])
		assert.Equal(t, "DAPR_SCHEDULER_JOB_NAME", detailsObject["reason"])
		require.Equal(t, ErrInfoType, detailsObject["@type"])
	})

	t.Run("delete job not found", func(t *testing.T) {
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/jobs/notfound", e.daprd.HTTPPort())

		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		// Deleting an unknown key is a success in etcd.
		require.Equal(t, http.StatusNoContent, resp.StatusCode)
	})

	// Covers GetJob job not found
	t.Run("get job not found", func(t *testing.T) {
		endpoint := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/jobs/notfound", e.daprd.HTTPPort())

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)

		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var data map[string]interface{}
		err = json.Unmarshal([]byte(string(body)), &data)
		require.NoError(t, err)

		// Confirm that the 'errorCode' field exists and contains the correct error code
		errCode, exists := data["errorCode"]
		require.True(t, exists)
		assert.Equal(t, "DAPR_SCHEDULER_GET_JOB", errCode)

		// Confirm that the 'message' field exists and contains the correct error message
		errMsg, exists := data["message"]
		require.True(t, exists)
		assert.Contains(t, errMsg, "failed to get job")

		// Confirm that the 'details' field exists and has one element
		details, exists := data["details"]
		require.True(t, exists)

		detailsArray, ok := details.([]interface{})
		require.True(t, ok)
		require.Len(t, detailsArray, 1)

		// Confirm that the first element of the 'details' array has the correct ErrorInfo details
		detailsObject, ok := detailsArray[0].(map[string]interface{})
		require.True(t, ok)
		require.Equal(t, "dapr.io", detailsObject["domain"])
		assert.Equal(t, "DAPR_SCHEDULER_GET_JOB", detailsObject["reason"])
		require.Equal(t, ErrInfoType, detailsObject["@type"])
	})
}
