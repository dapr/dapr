/*
Copyright 2026 The Dapr Authors
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
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/pkg/messages/errorcodes"
	kiterrors "github.com/dapr/kit/errors"
)

const helpLinkHealthChecks = "https://docs.dapr.io/operations/resiliency/health-checks/sidecar-health/"

type HealthError struct{}

func Health() *HealthError {
	return &HealthError{}
}

func (h *HealthError) NotReady(unhealthyTargets []string, appID string) error {
	msg := fmt.Sprintf("dapr is not ready: %v", unhealthyTargets)
	meta := map[string]string{
		"appID":                 appID,
		"unhealthyTargetCount":  strconv.Itoa(len(unhealthyTargets)),
		"unhealthyTargetNames":  strings.Join(unhealthyTargets, ","),
	}

	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		msg,
		errorcodes.HealthNotReady.Code,
		string(errorcodes.HealthNotReady.Category),
	).WithErrorInfo(errorcodes.HealthNotReady.Code, meta).
		WithHelpLink(helpLinkHealthChecks, "Review sidecar health checks and startup dependencies").
		WithDetails(&errdetails.PreconditionFailure{
			Violations: []*errdetails.PreconditionFailure_Violation{{
				Type:        "HEALTH_NOT_READY",
				Subject:     "dapr.runtime",
				Description: "One or more startup targets are unhealthy",
			}},
		}).
		Build()
}

func (h *HealthError) AppIDNotMatch(expectedAppID, requestedAppID string) error {
	msg := "dapr app-id does not match"
	meta := map[string]string{
		"expectedAppID":  expectedAppID,
		"requestedAppID": requestedAppID,
	}

	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		msg,
		errorcodes.HealthAppidNotMatch.Code,
		string(errorcodes.HealthAppidNotMatch.Category),
	).WithFieldViolation("appid", "requested appid must match the configured app id").
		WithErrorInfo(errorcodes.HealthAppidNotMatch.Code, meta).
		WithHelp([]*errdetails.Help_Link{{
			Description: "Pass the app id configured for this sidecar in the appid query parameter",
			Url:         helpLinkHealthChecks,
		}}).
		Build()
}

func (h *HealthError) OutboundNotReady(appID string) error {
	msg := "dapr outbound is not ready"
	meta := map[string]string{
		"appID": appID,
	}

	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		msg,
		errorcodes.HealthOutboundNotReady.Code,
		string(errorcodes.HealthOutboundNotReady.Category),
	).WithErrorInfo(errorcodes.HealthOutboundNotReady.Code, meta).
		WithHelpLink(helpLinkHealthChecks, "Check outbound dependencies such as app health and component connectivity").
		Build()
}
