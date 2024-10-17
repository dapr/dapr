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

package serialize

import (
	"testing"

	"github.com/stretchr/testify/assert"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

func Test_buildJobName(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		req     Request
		expName string
		expErr  bool
	}{
		"nil meta should return error": {
			req: &schedulerv1pb.ScheduleJobRequest{
				Name:     "test",
				Metadata: nil,
			},
			expName: "",
			expErr:  true,
		},
		"meta with nil target should return error": {
			req: &schedulerv1pb.ScheduleJobRequest{
				Name:     "test",
				Metadata: new(schedulerv1pb.JobMetadata),
			},
			expName: "",
			expErr:  true,
		},
		"job meta should return concatenated name": {
			req: &schedulerv1pb.ScheduleJobRequest{
				Name: "test",
				Metadata: &schedulerv1pb.JobMetadata{
					Namespace: "myns", AppId: "myapp",
					Target: &schedulerv1pb.JobTargetMetadata{
						Type: &schedulerv1pb.JobTargetMetadata_Job{
							Job: new(schedulerv1pb.TargetJob),
						},
					},
				},
			},
			expName: "app||myns||myapp||test",
			expErr:  false,
		},
		"nil job meta should return concatenated name": {
			req: &schedulerv1pb.ScheduleJobRequest{
				Name: "test",
				Metadata: &schedulerv1pb.JobMetadata{
					Namespace: "myns", AppId: "myapp",
					Target: &schedulerv1pb.JobTargetMetadata{
						Type: &schedulerv1pb.JobTargetMetadata_Job{
							Job: nil,
						},
					},
				},
			},
			expName: "app||myns||myapp||test",
			expErr:  false,
		},
		"actor meta should return concatenated name": {
			req: &schedulerv1pb.ScheduleJobRequest{
				Name: "test",
				Metadata: &schedulerv1pb.JobMetadata{
					Namespace: "myns", AppId: "myapp",
					Target: &schedulerv1pb.JobTargetMetadata{
						Type: &schedulerv1pb.JobTargetMetadata_Actor{
							Actor: &schedulerv1pb.TargetActorReminder{
								Type: "myactortype", Id: "myactorid",
							},
						},
					},
				},
			},
			expName: "actorreminder||myns||myactortype||myactorid||test",
			expErr:  false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := buildJobName(test.req)
			assert.Equal(t, test.expErr, err != nil)
			assert.Equal(t, test.expName, got)
		})
	}
}
