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

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/kit/ptr"
)

func Test_schedFPToCron(t *testing.T) {
	tests := map[string]struct {
		fp  *schedulerv1pb.FailurePolicy
		exp *api.FailurePolicy
	}{
		"nil": {
			fp:  nil,
			exp: nil,
		},
		"constant nil": {
			fp: &schedulerv1pb.FailurePolicy{
				Policy: &schedulerv1pb.FailurePolicy_Constant{
					Constant: &schedulerv1pb.FailurePolicyConstant{
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
			fp: &schedulerv1pb.FailurePolicy{
				Policy: &schedulerv1pb.FailurePolicy_Constant{
					Constant: &schedulerv1pb.FailurePolicyConstant{
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
			fp: &schedulerv1pb.FailurePolicy{
				Policy: &schedulerv1pb.FailurePolicy_Constant{
					Constant: &schedulerv1pb.FailurePolicyConstant{
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
			fp: &schedulerv1pb.FailurePolicy{
				Policy: &schedulerv1pb.FailurePolicy_Constant{
					Constant: &schedulerv1pb.FailurePolicyConstant{
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
			fp: &schedulerv1pb.FailurePolicy{
				Policy: &schedulerv1pb.FailurePolicy_Drop{
					Drop: new(schedulerv1pb.FailurePolicyDrop),
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
		exp *schedulerv1pb.FailurePolicy
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
			exp: &schedulerv1pb.FailurePolicy{
				Policy: &schedulerv1pb.FailurePolicy_Constant{
					Constant: &schedulerv1pb.FailurePolicyConstant{
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
			exp: &schedulerv1pb.FailurePolicy{
				Policy: &schedulerv1pb.FailurePolicy_Constant{
					Constant: &schedulerv1pb.FailurePolicyConstant{
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
			exp: &schedulerv1pb.FailurePolicy{
				Policy: &schedulerv1pb.FailurePolicy_Constant{
					Constant: &schedulerv1pb.FailurePolicyConstant{
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
			exp: &schedulerv1pb.FailurePolicy{
				Policy: &schedulerv1pb.FailurePolicy_Constant{
					Constant: &schedulerv1pb.FailurePolicyConstant{
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
			exp: &schedulerv1pb.FailurePolicy{
				Policy: &schedulerv1pb.FailurePolicy_Drop{
					Drop: new(schedulerv1pb.FailurePolicyDrop),
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
