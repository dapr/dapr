/*
Copyright 2026 The Dapr Authors
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

package orchestrator

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// errStandbyTurn refuses a reminder on a standby host (Options.Standby): it
// would run a workflow turn, and no worker is connected to run it. A standby
// host does not advertise the workflow actor type to the scheduler, so a
// reminder reaches it only when another daprd forwards one through a stale
// placement table while a worker connects. FailedPrecondition fails the
// trigger, and the reminder's retry policy delivers it again once the table
// routes the actor to the daprd with the worker.
func errStandbyTurn(actorID, reminder string) error {
	return status.Errorf(codes.FailedPrecondition,
		"workflow actor '%s' is on a standby host with no workflow worker connected; not running reminder '%s'",
		actorID, reminder)
}
