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
	"testing"
	"time"

	"github.com/diagridio/go-etcd-cron/api"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/durationpb"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	"github.com/dapr/kit/ptr"
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
						MaxRetries: ptr.Of(uint32(123)),
					},
				},
			},
			exp: &api.FailurePolicy{
				Policy: &api.FailurePolicy_Constant{
					Constant: &api.FailurePolicyConstant{
						Interval:   durationpb.New(time.Second * 3),
						MaxRetries: ptr.Of(uint32(123)),
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
						MaxRetries: ptr.Of(uint32(123)),
					},
				},
			},
			exp: &api.FailurePolicy{
				Policy: &api.FailurePolicy_Constant{
					Constant: &api.FailurePolicyConstant{
						Interval:   nil,
						MaxRetries: ptr.Of(uint32(123)),
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
						MaxRetries: ptr.Of(uint32(123)),
					},
				},
			},
			exp: &commonv1pb.JobFailurePolicy{
				Policy: &commonv1pb.JobFailurePolicy_Constant{
					Constant: &commonv1pb.JobFailurePolicyConstant{
						Interval:   durationpb.New(time.Second * 3),
						MaxRetries: ptr.Of(uint32(123)),
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
						MaxRetries: ptr.Of(uint32(123)),
					},
				},
			},
			exp: &commonv1pb.JobFailurePolicy{
				Policy: &commonv1pb.JobFailurePolicy_Constant{
					Constant: &commonv1pb.JobFailurePolicyConstant{
						Interval:   nil,
						MaxRetries: ptr.Of(uint32(123)),
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
