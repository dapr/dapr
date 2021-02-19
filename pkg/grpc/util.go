// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"

func stateConsistencyToString(c commonv1pb.StateOptions_StateConsistency) string {
	switch c {
	case commonv1pb.StateOptions_CONSISTENCY_EVENTUAL:
		return "eventual"
	case commonv1pb.StateOptions_CONSISTENCY_STRONG:
		return "strong"
	}

	return ""
}

func stateConcurrencyToString(c commonv1pb.StateOptions_StateConcurrency) string {
	switch c {
	case commonv1pb.StateOptions_CONCURRENCY_FIRST_WRITE:
		return "first-write"
	case commonv1pb.StateOptions_CONCURRENCY_LAST_WRITE:
		return "last-write"
	}

	return ""
}
