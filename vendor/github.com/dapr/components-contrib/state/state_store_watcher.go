// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package state

import "context"

const (
	WatchDiscardCreate   = "discard-create"
	WatchDiscardModify   = "discard-modify"
	WatchDiscardDelete   = "discard-delete"
	WatchDiscardChildren = "discard-children"
	WatchFromKey         = "from-key"
	WatchMatchingPrefix  = "matching-prefix"
	WatchInRange         = "in-range"
)

// StoreWatcher is an interface for initialization and support state watcher
type StoreWatcher interface {
	Init(metadata Metadata) error
	Watch(req *WatchStateRequest) (<-chan *Event, context.CancelFunc, error)
}

func Watch(watcher StoreWatcher, req *WatchStateRequest, handler func(*Event) error) (context.CancelFunc, error) {
	events, cancel, err := watcher.Watch(req)
	if err != nil {
		return nil, err
	}

	go func() {
		defer cancel()

		for event := range events {
			err = handler(event)
			if err != nil {
				break
			}
		}
	}()

	return cancel, nil
}
