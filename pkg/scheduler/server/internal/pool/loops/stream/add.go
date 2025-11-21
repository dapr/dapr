/*
Copyright 2025 The Dapr Authors
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

package stream

import (
	"context"
	"slices"

	"github.com/diagridio/go-etcd-cron/api"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

func handleAdd(ctx context.Context, cron api.Interface, add *schedulerv1pb.WatchJobsRequestInitial) (context.CancelCauseFunc, error) {
	var prefixes []string
	var actorTypes []string

	reqNamespace := add.GetNamespace()
	reqAppID := add.GetAppId()

	ts := add.GetAcceptJobTypes()
	if len(ts) == 0 || slices.Contains(ts, schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_JOB) {
		prefixes = append(prefixes, "app||"+reqNamespace+"||"+reqAppID+"||")
	}

	if len(ts) == 0 || slices.Contains(ts, schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER) {
		for _, actorType := range add.GetActorTypes() {
			log.Infof("Adding a Sidecar connection to Scheduler for actor type: %s/%s.", reqNamespace, actorType)
			prefixes = append(prefixes, "actorreminder||"+reqNamespace+"||"+actorType+"||")
		}

		actorTypes = add.GetActorTypes()
	}

	log.Infof("Adding a Sidecar connection to Scheduler for app: %s/%s (actorTypes=%v).", reqNamespace, reqAppID, actorTypes)

	log.Debugf("Marking deliverable prefixes for Sidecar connection: %s/%s: %v.",
		add.GetNamespace(), add.GetAppId(), prefixes)

	cancel, err := cron.DeliverablePrefixes(ctx, prefixes...)
	if err != nil {
		return nil, err
	}

	log.Debugf("Added a Sidecar connection to Scheduler for: %s/%s.",
		add.GetNamespace(), add.GetAppId())

	return cancel, nil
}
