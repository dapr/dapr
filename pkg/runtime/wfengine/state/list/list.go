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
	"errors"
	"fmt"
	"strings"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
)

type ListOptions struct {
	ComponentStore *compstore.ComponentStore
	Namespace      string
	AppID          string

	PageSize          *uint32
	ContinuationToken *string
}

type ListInstanceIDsResult struct {
	Keys              []string
	ContinuationToken *string
}

func ListInstanceIDs(ctx context.Context, opts ListOptions) (*ListInstanceIDsResult, error) {
	store, _, ok := opts.ComponentStore.GetStateStoreActor()
	if !ok {
		return nil, errors.New("no state store with actor support found")
	}

	ks, ok := store.(state.KeysLiker)
	if !ok {
		return nil, fmt.Errorf("state store %T does not support listing keys", store)
	}

	like := opts.AppID + "||" + todo.ActorTypePrefix + opts.Namespace + "." + opts.AppID + ".workflow||%||metadata"

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

		keys = append(keys, split[2])
	}

	return &ListInstanceIDsResult{
		Keys:              keys,
		ContinuationToken: resp.ContinuationToken,
	}, nil
}
