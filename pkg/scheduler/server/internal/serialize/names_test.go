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

func Test_QueueStateKey(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		jobName string
		ns      string
		appID   string
		ok      bool
	}{
		"jobs API job counts under its app": {
			jobName: "app||ns1||app1||myjob",
			ns:      "ns1", appID: "app1", ok: true,
		},
		"jobs API job whose name contains the delimiter": {
			jobName: "app||ns2||app1||my||job",
			ns:      "ns2", appID: "app1", ok: true,
		},
		"workflow reminder counts under the app in the actor type": {
			jobName: "actorreminder||ns3||dapr.internal.ns3.wfapp.workflow||inst1||start-123",
			ns:      "ns3", appID: "wfapp", ok: true,
		},
		"activity reminder counts under the app in the actor type": {
			jobName: "actorreminder||ns4||dapr.internal.ns4.actapp.activity||inst1::0::1||run-activity",
			ns:      "ns4", appID: "actapp", ok: true,
		},
		"app ID containing dots": {
			jobName: "actorreminder||ns5||dapr.internal.ns5.my.app.workflow||inst1||timer",
			ns:      "ns5", appID: "my.app", ok: true,
		},
		"user actor reminder is not counted": {
			jobName: "actorreminder||ns1||myactortype||id1||reminder",
		},
		"internal actor type for another namespace is not counted": {
			jobName: "actorreminder||ns1||dapr.internal.ns2.app1.workflow||inst1||timer",
		},
		"internal actor type that is neither workflow nor activity is not counted": {
			jobName: "actorreminder||ns1||dapr.internal.ns1.app1.other||inst1||timer",
		},
		"internal actor type with no app is not counted": {
			jobName: "actorreminder||ns1||dapr.internal.ns1..workflow||inst1||timer",
		},
		"unknown job type is not counted": {
			jobName: "other||ns1||app1||job",
		},
		"malformed name is not counted": {
			jobName: "app||ns1||app1",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ns, appID, ok := QueueStateKey(test.jobName)
			assert.Equal(t, test.ok, ok)
			assert.Equal(t, test.ns, ns)
			assert.Equal(t, test.appID, appID)
		})
	}
}
