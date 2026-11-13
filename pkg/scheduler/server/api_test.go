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

package server

import (
	"math"
	"testing"
	"time"

	"github.com/diagridio/go-etcd-cron/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

func Test_schedFPToCron(t *testing.T) {
	tests := map[string]struct {
		fp  *commonv1pb.JobFailurePolicy
		exp *api.FailurePolicy
	}{
		"nil": {
			fp:  nil,
			exp: nil,
		},
		"constant nil": {
			fp: &commonv1pb.JobFailurePolicy{
				Policy: &commonv1pb.JobFailurePolicy_Constant{
					Constant: &commonv1pb.JobFailurePolicyConstant{
						Interval:   nil,
						MaxRetries: nil,
					},
				},
			},
			exp: &api.FailurePolicy{
				Policy: &api.FailurePolicy_Constant{
					Constant: &api.FailurePolicyConstant{
						Interval:   nil,
						MaxRetries: nil,
					},
				},
			},
		},
		"constant set": {
			fp: &commonv1pb.JobFailurePolicy{
				Policy: &commonv1pb.JobFailurePolicy_Constant{
					Constant: &commonv1pb.JobFailurePolicyConstant{
						Interval:   durationpb.New(time.Second * 3),
						MaxRetries: new(uint32(123)),
					},
				},
			},
			exp: &api.FailurePolicy{
				Policy: &api.FailurePolicy_Constant{
					Constant: &api.FailurePolicyConstant{
						Interval:   durationpb.New(time.Second * 3),
						MaxRetries: new(uint32(123)),
					},
				},
			},
		},
		"constant mixed 1": {
			fp: &commonv1pb.JobFailurePolicy{
				Policy: &commonv1pb.JobFailurePolicy_Constant{
					Constant: &commonv1pb.JobFailurePolicyConstant{
						Interval:   durationpb.New(time.Second * 3),
						MaxRetries: nil,
					},
				},
			},
			exp: &api.FailurePolicy{
				Policy: &api.FailurePolicy_Constant{
					Constant: &api.FailurePolicyConstant{
						Interval:   durationpb.New(time.Second * 3),
						MaxRetries: nil,
					},
				},
			},
		},
		"constant mixed 2": {
			fp: &commonv1pb.JobFailurePolicy{
				Policy: &commonv1pb.JobFailurePolicy_Constant{
					Constant: &commonv1pb.JobFailurePolicyConstant{
						Interval:   nil,
						MaxRetries: new(uint32(123)),
					},
				},
			},
			exp: &api.FailurePolicy{
				Policy: &api.FailurePolicy_Constant{
					Constant: &api.FailurePolicyConstant{
						Interval:   nil,
						MaxRetries: new(uint32(123)),
					},
				},
			},
		},
		"drop": {
			fp: &commonv1pb.JobFailurePolicy{
				Policy: &commonv1pb.JobFailurePolicy_Drop{
					Drop: new(commonv1pb.JobFailurePolicyDrop),
				},
			},
			exp: &api.FailurePolicy{
				Policy: &api.FailurePolicy_Drop{
					Drop: new(api.FailurePolicyDrop),
				},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, test.exp, schedFPToCron(test.fp))
		})
	}
}

func Test_cronFPToSched(t *testing.T) {
	tests := map[string]struct {
		fp  *api.FailurePolicy
		exp *commonv1pb.JobFailurePolicy
	}{
		"nil": {
			fp:  nil,
			exp: nil,
		},
		"constant nil": {
			fp: &api.FailurePolicy{
				Policy: &api.FailurePolicy_Constant{
					Constant: &api.FailurePolicyConstant{
						Interval:   nil,
						MaxRetries: nil,
					},
				},
			},
			exp: &commonv1pb.JobFailurePolicy{
				Policy: &commonv1pb.JobFailurePolicy_Constant{
					Constant: &commonv1pb.JobFailurePolicyConstant{
						Interval:   nil,
						MaxRetries: nil,
					},
				},
			},
		},
		"constant set": {
			fp: &api.FailurePolicy{
				Policy: &api.FailurePolicy_Constant{
					Constant: &api.FailurePolicyConstant{
						Interval:   durationpb.New(time.Second * 3),
						MaxRetries: new(uint32(123)),
					},
				},
			},
			exp: &commonv1pb.JobFailurePolicy{
				Policy: &commonv1pb.JobFailurePolicy_Constant{
					Constant: &commonv1pb.JobFailurePolicyConstant{
						Interval:   durationpb.New(time.Second * 3),
						MaxRetries: new(uint32(123)),
					},
				},
			},
		},
		"constant mixed 1": {
			fp: &api.FailurePolicy{
				Policy: &api.FailurePolicy_Constant{
					Constant: &api.FailurePolicyConstant{
						Interval:   durationpb.New(time.Second * 3),
						MaxRetries: nil,
					},
				},
			},
			exp: &commonv1pb.JobFailurePolicy{
				Policy: &commonv1pb.JobFailurePolicy_Constant{
					Constant: &commonv1pb.JobFailurePolicyConstant{
						Interval:   durationpb.New(time.Second * 3),
						MaxRetries: nil,
					},
				},
			},
		},
		"constant mixed 2": {
			fp: &api.FailurePolicy{
				Policy: &api.FailurePolicy_Constant{
					Constant: &api.FailurePolicyConstant{
						Interval:   nil,
						MaxRetries: new(uint32(123)),
					},
				},
			},
			exp: &commonv1pb.JobFailurePolicy{
				Policy: &commonv1pb.JobFailurePolicy_Constant{
					Constant: &commonv1pb.JobFailurePolicyConstant{
						Interval:   nil,
						MaxRetries: new(uint32(123)),
					},
				},
			},
		},
		"drop": {
			fp: &api.FailurePolicy{
				Policy: &api.FailurePolicy_Drop{
					Drop: new(api.FailurePolicyDrop),
				},
			},
			exp: &commonv1pb.JobFailurePolicy{
				Policy: &commonv1pb.JobFailurePolicy_Drop{
					Drop: new(commonv1pb.JobFailurePolicyDrop),
				},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, test.exp, cronFPToSched(test.fp))
		})
	}
}

func Test_validatePriority(t *testing.T) {
	meta := func(level schedulerv1pb.PriorityLevel, weight float32) *schedulerv1pb.JobMetadata {
		m := &schedulerv1pb.JobMetadata{}
		if level != schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_UNSPECIFIED {
			m.PriorityLevel = level.Enum()
		}
		if weight != 0 {
			m.FairnessWeight = &weight
		}
		return m
	}

	require.NoError(t, validatePriority(nil))
	require.NoError(t, validatePriority(meta(schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_UNSPECIFIED, 0)))
	for _, level := range []schedulerv1pb.PriorityLevel{
		schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_CRITICAL,
		schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_HIGH,
		schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_NORMAL,
		schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_LOW,
		schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_BACKGROUND,
	} {
		require.NoError(t, validatePriority(meta(level, 1)), level.String())
	}
	for _, weight := range []float32{0.001, 0.5, 1, 1000} {
		require.NoError(t, validatePriority(meta(schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_NORMAL, weight)))
	}

	for _, level := range []schedulerv1pb.PriorityLevel{-1, 6, 100} {
		err := validatePriority(meta(level, 0))
		require.Equal(t, codes.InvalidArgument, status.Code(err), level.String())
		require.ErrorContains(t, err, "not a known level")
	}
	for _, weight := range []float32{0.0001, -1, 1000.5, float32(math.Inf(1)), float32(math.NaN())} {
		err := validatePriority(meta(schedulerv1pb.PriorityLevel_PRIORITY_LEVEL_UNSPECIFIED, weight))
		require.Equal(t, codes.InvalidArgument, status.Code(err), "%g", weight)
		require.ErrorContains(t, err, "must be between 0.001 and 1000")
	}

	// An explicit 0 is set, so it is outside the range, unlike unset.
	err := validatePriority(&schedulerv1pb.JobMetadata{FairnessWeight: new(float32(0))})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.ErrorContains(t, err, "fairness_weight 0 must be between 0.001 and 1000")
}
