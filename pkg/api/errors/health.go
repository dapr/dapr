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
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/pkg/messages/errorcodes"
	kiterrors "github.com/dapr/kit/errors"
)

func HealthNotReady(unhealthyTargets []string) error {
	targets := slices.Clone(unhealthyTargets)
	sort.Strings(targets)

	metadata := map[string]string{
		"unhealthyTargetsCount": fmt.Sprintf("%d", len(targets)),
	}
	if len(targets) > 0 {
		metadata["unhealthyTargets"] = strings.Join(targets, ",")
	}

	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		fmt.Sprintf("dapr is not ready: %v", targets),
		"",
		string(errorcodes.HealthNotReady.Category),
	).
		WithHelpLink("https://docs.dapr.io/operations/resiliency/health-checks/", "Troubleshoot Dapr and app health checks.").
		WithErrorInfo(errorcodes.HealthNotReady.Code, metadata).
		Build()
}

func HealthAppIDNotMatch(appID, requestAppID string) error {
	builder := kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		"dapr app-id does not match",
		"",
		string(errorcodes.HealthAppidNotMatch.Category),
	)

	if requestAppID != "" {
		builder = builder.WithFieldViolation("appid", fmt.Sprintf("requested appid %q does not match configured app id %q", requestAppID, appID))
	}

	return builder.
		WithHelpLink("https://docs.dapr.io/operations/resiliency/health-checks/", "Use an appid query parameter that matches the configured app ID for this sidecar.").
		WithErrorInfo(errorcodes.HealthAppidNotMatch.Code, map[string]string{
			"appID":          appID,
			"requestedAppID": requestAppID,
		}).
		Build()
}

func HealthOutboundNotReady() error {
	return kiterrors.NewBuilder(
		codes.Internal,
		http.StatusInternalServerError,
		"dapr outbound is not ready",
		"",
		string(errorcodes.HealthOutboundNotReady.Category),
	).
		WithHelpLink("https://docs.dapr.io/operations/resiliency/health-checks/", "Troubleshoot Dapr outbound health checks.").
		WithErrorInfo(errorcodes.HealthOutboundNotReady.Code, nil).
		Build()
}
