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
	"google.golang.org/protobuf/proto"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

func Test_MetadataFromKey(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		key     string
		expMeta *schedulerv1pb.JobMetadata
		expErr  bool
	}{
		"an empty key should error": {
			key:     "",
			expMeta: nil,
			expErr:  true,
		},
		"a random key should error": {
			key:     "random",
			expMeta: nil,
			expErr:  true,
		},
		"a bad reminder should error": {
			key:     "foo||bar||actorreminde||myns||myactortype||myactorid||myremindername",
			expMeta: nil,
			expErr:  true,
		},
		"a no-namespace actor reminder should return metadata": {
			key: "actorreminder||myns||myactortype||myactorid||myremindername",
			expMeta: &schedulerv1pb.JobMetadata{
				AppId: "", Namespace: "myns",
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Actor{
						Actor: &schedulerv1pb.TargetActorReminder{
							Id: "myactorid", Type: "myactortype",
						},
					},
				},
			},
		},
		"a namespace actor reminder should return metadata": {
			key: "foo/bar/actorreminder||myns||myactortype||myactorid||myremindername",
			expMeta: &schedulerv1pb.JobMetadata{
				AppId: "", Namespace: "myns",
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Actor{
						Actor: &schedulerv1pb.TargetActorReminder{
							Id: "myactorid", Type: "myactortype",
						},
					},
				},
			},
		},
		"a namespace (with separator) actor reminder should return metadata": {
			key: "foo||bar/actorreminder||myns||myactortype||myactorid||myremindername",
			expMeta: &schedulerv1pb.JobMetadata{
				AppId: "", Namespace: "myns",
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Actor{
						Actor: &schedulerv1pb.TargetActorReminder{
							Id: "myactorid", Type: "myactortype",
						},
					},
				},
			},
		},
		"a namespace (with separator 2) actor reminder should return metadata": {
			key: "foo||bar||actorreminder||myns||myactortype||myactorid||myremindername",
			expMeta: &schedulerv1pb.JobMetadata{
				AppId: "", Namespace: "myns",
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Actor{
						Actor: &schedulerv1pb.TargetActorReminder{
							Id: "myactorid", Type: "myactortype",
						},
					},
				},
			},
		},
		"a no-namespace job should return metadata": {
			key: "app||myns||myid||jobname",
			expMeta: &schedulerv1pb.JobMetadata{
				AppId: "myid", Namespace: "myns",
				Target: &schedulerv1pb.JobTargetMetadata{Type: new(schedulerv1pb.JobTargetMetadata_Job)},
			},
		},
		"a namespace job should return metadata": {
			key: "foo/bar/app||myns||myid||jobname",
			expMeta: &schedulerv1pb.JobMetadata{
				AppId: "myid", Namespace: "myns",
				Target: &schedulerv1pb.JobTargetMetadata{Type: new(schedulerv1pb.JobTargetMetadata_Job)},
			},
		},
		"a namespace (with separator) job should return metadata": {
			key: "foo||bar/app||myns||myid||jobname",
			expMeta: &schedulerv1pb.JobMetadata{
				AppId: "myid", Namespace: "myns",
				Target: &schedulerv1pb.JobTargetMetadata{Type: new(schedulerv1pb.JobTargetMetadata_Job)},
			},
		},
		"a namespace (with separator 2) job should return metadata": {
			key: "foo||bar||app||myns||myid||jobname",
			expMeta: &schedulerv1pb.JobMetadata{
				AppId: "myid", Namespace: "myns",
				Target: &schedulerv1pb.JobTargetMetadata{Type: new(schedulerv1pb.JobTargetMetadata_Job)},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := MetadataFromKey(test.key)
			assert.Equal(t, test.expErr, err != nil)
			assert.True(t, proto.Equal(test.expMeta, got), "expected: %v, got: %v", test.expMeta, got)
		})
	}
}
