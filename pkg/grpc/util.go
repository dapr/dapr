/*
Copyright 2021 The Dapr Authors
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

package grpc

import (
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
)

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
