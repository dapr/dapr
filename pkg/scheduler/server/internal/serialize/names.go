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
	"fmt"
	"path"
	"strings"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

// PrefixesFromNamespace returns key prefixes for all jobs types for a given
// namespace.
func PrefixesFromNamespace(namespace string) []string {
	return []string{
		"actorreminder||" + namespace + "||",
		"app||" + namespace + "||",
	}
}

// MetadataFromKey returns the JobMetadata based on a raw job key.
func MetadataFromKey(key string) (*schedulerv1pb.JobMetadata, error) {
	seg := strings.Split(key, "||")

	if len(seg) >= 5 && path.Base(seg[len(seg)-5]) == "actorreminder" {
		seg = seg[len(seg)-4:]
		return &schedulerv1pb.JobMetadata{
			Namespace: seg[0],
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{
						Type: seg[1],
						Id:   seg[2],
					},
				},
			},
		}, nil
	}

	if len(seg) >= 4 && path.Base(seg[len(seg)-4]) == "app" {
		seg = seg[len(seg)-3:]
		return &schedulerv1pb.JobMetadata{
			Namespace: seg[0],
			AppId:     seg[1],
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: new(schedulerv1pb.JobTargetMetadata_Job),
			},
		}, nil
	}

	return nil, fmt.Errorf("invalid key: %s", key)
}
