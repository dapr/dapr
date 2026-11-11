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

package workflow

import (
	"context"
	"slices"

	"github.com/stretchr/testify/require"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
)

// ScheduleRuns returns the instance IDs of a workflow schedule's runs,
// oldest first. Inside an EventuallyWithT, pass its CollectT so an error is
// retried.
func ScheduleRuns(t require.TestingT, ctx context.Context, client *client.TaskHubGrpcClient, name string) []string {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}
	resp, err := client.ListInstanceIDs(ctx, api.WithListInstanceIDsInstanceIDPrefix(name+"_"))
	require.NoError(t, err)
	ids := resp.GetInstanceIds()
	slices.Sort(ids)
	return ids
}
