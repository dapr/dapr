// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package state

import "context"

const (
	WatchDiscardCreate  = "discard-create"
	WatchDiscardModify  = "discard-modify"
	WatchDiscardDelete  = "discard-delete"
	WatchFromKey        = "from-key"
	WatchMatchingPrefix = "matching-prefix"
	WatchInRange        = "in-range"
)

// StoreWatcher is an interface for initialization and support state watcher
type StoreWatcher interface {
	Init(metadata Metadata) error
	Watch(req *WatchStateRequest) (<-chan *Event, context.CancelFunc, error)
}
