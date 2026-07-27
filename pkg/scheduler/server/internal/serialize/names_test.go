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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

func actorMeta(namespace, actorType, actorID string) *schedulerv1pb.JobMetadata {
	return &schedulerv1pb.JobMetadata{
		Namespace: namespace,
		Target: &schedulerv1pb.JobTargetMetadata{
			Type: &schedulerv1pb.JobTargetMetadata_Actor{
				Actor: &schedulerv1pb.TargetActorReminder{Type: actorType, Id: actorID},
			},
		},
	}
}

func appMeta(namespace, appID string) *schedulerv1pb.JobMetadata {
	return &schedulerv1pb.JobMetadata{
		Namespace: namespace,
		AppId:     appID,
		Target:    &schedulerv1pb.JobTargetMetadata{Type: new(schedulerv1pb.JobTargetMetadata_Job)},
	}
}

func Test_PrefixFromMetadata(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		meta      *schedulerv1pb.JobMetadata
		expPrefix string
		expErr    bool
	}{
		"actor reminder": {
			meta:      actorMeta("myns", "myactortype", "myactorid"),
			expPrefix: "actorreminder||myns||myactortype||myactorid||",
		},
		"app job": {
			meta:      appMeta("myns", "myid"),
			expPrefix: "app||myns||myid||",
		},
		"actor id containing the || delimiter and single pipes": {
			meta:      actorMeta("myns", "SmokeDetectorActor", "nexus-fire-safety-api||SmokeDetectorGroupMonitorActor||Nexus|6671cfbe7f48af247700a24b||SmokeDetectorGroupInformation"),
			expPrefix: "actorreminder||myns||SmokeDetectorActor||nexus-fire-safety-api||SmokeDetectorGroupMonitorActor||Nexus|6671cfbe7f48af247700a24b||SmokeDetectorGroupInformation||",
		},
		"unknown target type errors": {
			meta:   &schedulerv1pb.JobMetadata{Namespace: "myns"},
			expErr: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			prefix, err := PrefixFromMetadata(test.meta)
			assert.Equal(t, test.expErr, err != nil, "%v", err)
			if !test.expErr {
				assert.Equal(t, test.expPrefix, prefix)
			}
		})
	}
}

// Test_PrefixFromMetadata_RoundTrip proves that trimming the prefix off a full
// job key recovers the reminder name unambiguously, even when the actor id and
// the reminder name themselves contain "|" and "||" (the field delimiter).
func Test_PrefixFromMetadata_RoundTrip(t *testing.T) {
	t.Parallel()

	cases := map[string]reqAdapter{
		"simple actor reminder": {
			name: "myremindername",
			meta: actorMeta("myns", "myactortype", "myactorid"),
		},
		"schreder: pipes in id, pipe and at in name": {
			name: "my@reminder|name",
			meta: actorMeta("dapr-tests", "SmokeDetectorActor", "nexus-fire-safety-api||SmokeDetectorGroupMonitorActor||Nexus|6671cfbe7f48af247700a24b||SmokeDetectorGroupInformation"),
		},
		"app job with pipes in name": {
			name: "my|job@name",
			meta: appMeta("myns", "myappid"),
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fullKey, err := buildJobName(tc)
			require.NoError(t, err)

			prefix, err := PrefixFromMetadata(tc.meta)
			require.NoError(t, err)

			require.True(t, strings.HasPrefix(fullKey, prefix), "key %q must start with prefix %q", fullKey, prefix)
			assert.Equal(t, tc.name, strings.TrimPrefix(fullKey, prefix))
		})
	}
}

type reqAdapter struct {
	name string
	meta *schedulerv1pb.JobMetadata
}

func (r reqAdapter) GetName() string                         { return r.name }
func (r reqAdapter) GetMetadata() *schedulerv1pb.JobMetadata { return r.meta }
