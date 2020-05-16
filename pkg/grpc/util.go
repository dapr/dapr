// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"

func stateConsistencyToString(c commonv1pb.StateOptions_StateConsistency) string {
	switch c {
	case commonv1pb.StateOptions_EVENTUAL:
		return "eventual"
	case commonv1pb.StateOptions_STRONG:
		return "string"
	}

	return ""
}

func stateConcurrencyToString(c commonv1pb.StateOptions_StateConcurrency) string {
	switch c {
	case commonv1pb.StateOptions_FIRST_WRITE:
		return "first-write"
	case commonv1pb.StateOptions_LAST_WRITE:
		return "last-write"
	}

	return ""
}

func retryPatternToString(r commonv1pb.StateRetryPolicy_RetryPattern) string {
	switch r {
	case commonv1pb.StateRetryPolicy_LINEAR:
		return "linear"
	case commonv1pb.StateRetryPolicy_EXPONENTIAL:
		return "exponential"
	}

	return ""
}
