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
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
)

// ListInstancePages calls ListInstances until the continuation token is nil,
// and returns each page.
func ListInstancePages(t *testing.T, ctx context.Context, c *client.TaskHubGrpcClient, opts ...api.ListInstancesOptions) [][]*protos.WorkflowInstanceSummary {
	t.Helper()
	var pages [][]*protos.WorkflowInstanceSummary
	var tok *string
	for {
		o := opts
		if tok != nil {
			o = append(slices.Clone(opts), api.WithListInstancesContinuationToken(*tok))
		}
		resp, err := c.ListInstances(ctx, o...)
		require.NoError(t, err)
		pages = append(pages, resp.GetInstances())
		if resp.ContinuationToken == nil {
			return pages
		}
		tok = resp.ContinuationToken
		require.Less(t, len(pages), 1000, "the list does not end")
	}
}

// ListAllInstances returns the instances of all ListInstances pages.
func ListAllInstances(t *testing.T, ctx context.Context, c *client.TaskHubGrpcClient, opts ...api.ListInstancesOptions) []*protos.WorkflowInstanceSummary {
	t.Helper()
	var out []*protos.WorkflowInstanceSummary
	for _, p := range ListInstancePages(t, ctx, c, opts...) {
		out = append(out, p...)
	}
	return out
}

// ListedIDs returns the IDs of the instances of all ListInstances pages.
func ListedIDs(t *testing.T, ctx context.Context, c *client.TaskHubGrpcClient, opts ...api.ListInstancesOptions) []string {
	t.Helper()
	return SummaryIDs(ListAllInstances(t, ctx, c, opts...))
}

// SummaryIDs returns the instance IDs of the summaries.
func SummaryIDs(in []*protos.WorkflowInstanceSummary) []string {
	ids := make([]string, 0, len(in))
	for _, s := range in {
		ids = append(ids, s.GetInstanceId())
	}
	return ids
}

// AssertSummaryMatchesMetadata asserts that the summary of an instance has
// the values that GetInstance returns for it.
func AssertSummaryMatchesMetadata(t *testing.T, ctx context.Context, c *client.TaskHubGrpcClient, sum *protos.WorkflowInstanceSummary) {
	t.Helper()
	id := sum.GetInstanceId()
	meta, err := c.FetchWorkflowMetadata(ctx, api.InstanceID(id))
	require.NoError(t, err)
	assert.Equal(t, meta.GetName(), sum.GetName(), id)
	assert.Equal(t, meta.GetRuntimeStatus(), sum.GetRuntimeStatus(), id)
	assert.Truef(t, proto.Equal(meta.GetCreatedAt(), sum.GetCreatedTimestamp()), "%s createdAt: %v != %v", id, meta.GetCreatedAt(), sum.GetCreatedTimestamp())
	assert.Truef(t, proto.Equal(meta.GetStartedAt(), sum.GetStartedAt()), "%s startedAt: %v != %v", id, meta.GetStartedAt(), sum.GetStartedAt())
	assert.Truef(t, proto.Equal(meta.GetLastUpdatedAt(), sum.GetLastUpdatedTimestamp()), "%s lastUpdatedAt: %v != %v", id, meta.GetLastUpdatedAt(), sum.GetLastUpdatedTimestamp())
	assert.Truef(t, proto.Equal(meta.GetCompletedAt(), sum.GetCompletedTimestamp()), "%s completedAt: %v != %v", id, meta.GetCompletedAt(), sum.GetCompletedTimestamp())
	assert.Equal(t, meta.GetVersion().GetValue(), sum.GetVersion(), id)
}

// EventuallyListedIDs waits until ListInstances returns exactly the IDs in
// want.
func EventuallyListedIDs(t *testing.T, ctx context.Context, c *client.TaskHubGrpcClient, want []string, opts ...api.ListInstancesOptions) {
	t.Helper()
	assert.EventuallyWithT(t, func(ct *assert.CollectT) {
		ids, err := tryListedIDs(ctx, c, opts...)
		if assert.NoError(ct, err) {
			assert.ElementsMatch(ct, want, ids)
		}
	}, 5*time.Second, 10*time.Millisecond)
}

// tryListedIDs is ListedIDs for use inside EventuallyWithT.
func tryListedIDs(ctx context.Context, c *client.TaskHubGrpcClient, opts ...api.ListInstancesOptions) ([]string, error) {
	var ids []string
	var tok *string
	for range 1000 {
		o := opts
		if tok != nil {
			o = append(slices.Clone(opts), api.WithListInstancesContinuationToken(*tok))
		}
		resp, err := c.ListInstances(ctx, o...)
		if err != nil {
			return nil, err
		}
		ids = append(ids, SummaryIDs(resp.GetInstances())...)
		if resp.ContinuationToken == nil {
			return ids, nil
		}
		tok = resp.ContinuationToken
	}
	return nil, errors.New("the list does not end")
}
