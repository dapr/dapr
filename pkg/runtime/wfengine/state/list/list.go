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

package list

import (
	"context"
	"fmt"
	"strings"

	"github.com/dapr/components-contrib/state"
	actorstate "github.com/dapr/dapr/pkg/actors/state"
)

type ListOptions struct {
	// StoreResolver resolves the state store of the workflow actor type,
	// which is the default actor state store unless spec.workflow.stateStore
	// binds another.
	StoreResolver actorstate.StoreResolver
	// WorkflowActorType is the app's workflow actor type, whose state store
	// holds the instances.
	WorkflowActorType string
	AppID             string
	// IDPrefix, when set, lists only the instance IDs starting with it.
	IDPrefix string

	PageSize          *uint32
	ContinuationToken *string
}

type ListInstanceIDsResult struct {
	Keys              []string
	ContinuationToken *string
}

func ListInstanceIDs(ctx context.Context, opts ListOptions) (*ListInstanceIDsResult, error) {
	storeName, store, err := opts.StoreResolver.Resolve(opts.WorkflowActorType)
	if err != nil {
		return nil, err
	}

	ks, ok := store.(state.KeysLiker)
	if !ok {
		return nil, fmt.Errorf("state store %s does not support listing keys", storeName)
	}

	// The pattern holds the prefix only up to its first LIKE metacharacter,
	// as stores escape them differently; the filter below drops the extra
	// matches. A page can come back short as a result.
	likePrefix := opts.IDPrefix
	if i := strings.IndexAny(likePrefix, `%_\`); i >= 0 {
		likePrefix = likePrefix[:i]
	}
	like := opts.AppID + "||" + opts.WorkflowActorType + "||" + likePrefix + "%||metadata"

	resp, err := ks.KeysLike(ctx, &state.KeysLikeRequest{
		Pattern:           like,
		ContinuationToken: opts.ContinuationToken,
		PageSize:          opts.PageSize,
	})
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(resp.Keys))
	for _, key := range resp.Keys {
		split := strings.Split(key, "||")
		if len(split) != 4 {
			return nil, fmt.Errorf("invalid key format: %s", key)
		}

		if strings.HasPrefix(split[2], opts.IDPrefix) {
			keys = append(keys, split[2])
		}
	}

	return &ListInstanceIDsResult{
		Keys:              keys,
		ContinuationToken: resp.ContinuationToken,
	}, nil
}
