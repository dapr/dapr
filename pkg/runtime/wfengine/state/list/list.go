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
	wfstate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
)

type ListOptions struct {
	ComponentStore *compstore.ComponentStore
	Namespace      string
	AppID          string
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
	_, ks, err := actorStore(opts.ComponentStore)
	if err != nil {
		return nil, err
	}

	page, err := keysPage(ctx, ks, scope{
		namespace: opts.Namespace,
		appID:     opts.AppID,
		idPrefix:  opts.IDPrefix,
	}, opts.ContinuationToken, opts.PageSize)
	if err != nil {
		return nil, err
	}

	keys := make([]string, len(page.entries))
	for i, e := range page.entries {
		keys[i] = e.id
	}

	return &ListInstanceIDsResult{
		Keys:              keys,
		ContinuationToken: page.token,
	}, nil
}

// actorStore returns the actor state store, which must support KeysLike.
func actorStore(cs *compstore.ComponentStore) (state.Store, state.KeysLiker, error) {
	store, _, ok := cs.GetStateStoreActor()
	if !ok {
		return nil, nil, errors.New("no state store with actor support found")
	}

	ks, ok := store.(state.KeysLiker)
	if !ok {
		return nil, nil, fmt.Errorf("state store %T does not support listing keys", store)
	}

	return store, ks, nil
}

// scope is the set of workflow instances that a list call covers.
type scope struct {
	namespace string
	appID     string
	idPrefix  string
}

// keyEntry is the metadata row key of one instance in a page of keys.
type keyEntry struct {
	key string
	id  string
}

// keyPage is one page of metadata row keys, after the filters below.
type keyPage struct {
	entries []keyEntry
	token   *string
}

// keysPage reads one page of metadata row keys from the store. It keeps the
// keys of the instances of this app whose ID starts with the prefix.
func keysPage(ctx context.Context, ks state.KeysLiker, sc scope, token *string, pageSize *uint32) (*keyPage, error) {
	// The pattern holds the prefix only up to its first LIKE metacharacter,
	// as stores escape them differently; the filter below drops the extra
	// matches. A page can come back short as a result.
	likePrefix := sc.idPrefix
	if i := strings.IndexAny(likePrefix, `%_\`); i >= 0 {
		likePrefix = likePrefix[:i]
	}
	keyPrefix := sc.appID + "||" + todo.ActorTypePrefix + sc.namespace + "." + sc.appID + ".workflow||"
	keySuffix := "||" + wfstate.MetadataKey
	like := keyPrefix + likePrefix + "%" + keySuffix

	resp, err := ks.KeysLike(ctx, &state.KeysLikeRequest{
		Pattern:           like,
		ContinuationToken: token,
		PageSize:          pageSize,
	})
	if err != nil {
		return nil, err
	}

	page := &keyPage{
		entries: make([]keyEntry, 0, len(resp.Keys)),
		token:   resp.ContinuationToken,
	}
	for _, key := range resp.Keys {
		// The app ID and the namespace are not escaped in the pattern either.
		// An "_" in them matches any character, so the store can return the
		// keys of another app that shares it. Some stores also match LIKE
		// without case. Only the keys of this app's workflow actor type are
		// kept.
		if !strings.HasPrefix(key, keyPrefix) || !strings.HasSuffix(key, keySuffix) {
			continue
		}

		split := strings.Split(key, "||")
		if len(split) != 4 {
			return nil, fmt.Errorf("invalid key format: %s", key)
		}

		if strings.HasPrefix(split[2], sc.idPrefix) {
			page.entries = append(page.entries, keyEntry{key: key, id: split[2]})
		}
	}

	return page, nil
}
