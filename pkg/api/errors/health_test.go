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

package errors

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestHealthNotReady(t *testing.T) {
	err := HealthNotReady([]string{"target-b", "target-a"})
	s, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.Internal, s.Code())
	require.Equal(t, "dapr is not ready: [target-a target-b]", s.Message())
	require.NotEmpty(t, s.Details())

	var errInfo *errdetails.ErrorInfo
	for _, detail := range s.Details() {
		if d, ok := detail.(*errdetails.ErrorInfo); ok {
			errInfo = d
			break
		}
	}
	require.NotNil(t, errInfo)
	require.Equal(t, "ERR_HEALTH_NOT_READY", errInfo.GetReason())
	require.Equal(t, "2", errInfo.GetMetadata()["unhealthyTargetsCount"])
	require.Equal(t, "target-a,target-b", errInfo.GetMetadata()["unhealthyTargets"])
}

func TestHealthAppIDNotMatch(t *testing.T) {
	err := HealthAppIDNotMatch("app1", "app2")
	s, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.Internal, s.Code())
	require.Equal(t, "dapr app-id does not match", s.Message())
	require.NotEmpty(t, s.Details())

	var errInfo *errdetails.ErrorInfo
	for _, detail := range s.Details() {
		if d, ok := detail.(*errdetails.ErrorInfo); ok {
			errInfo = d
			break
		}
	}
	require.NotNil(t, errInfo)
	require.Equal(t, "ERR_HEALTH_APPID_NOT_MATCH", errInfo.GetReason())
	require.Equal(t, "app1", errInfo.GetMetadata()["appID"])
	require.Equal(t, "app2", errInfo.GetMetadata()["requestedAppID"])
}

func TestHealthOutboundNotReady(t *testing.T) {
	err := HealthOutboundNotReady()
	s, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.Internal, s.Code())
	require.Equal(t, "dapr outbound is not ready", s.Message())
	require.NotEmpty(t, s.Details())

	var errInfo *errdetails.ErrorInfo
	for _, detail := range s.Details() {
		if d, ok := detail.(*errdetails.ErrorInfo); ok {
			errInfo = d
			break
		}
	}
	require.NotNil(t, errInfo)
	require.Equal(t, "ERR_OUTBOUND_HEALTH_NOT_READY", errInfo.GetReason())
}
