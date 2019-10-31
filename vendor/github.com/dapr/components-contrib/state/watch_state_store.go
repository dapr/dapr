// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package state

const (
	WatchDiscardCreate  = "discard-create"
	WatchDiscardModify  = "discard-modify"
	WatchDiscardDelete  = "discard-delete"
	WatchFromKey        = "from-key"
	WatchMatchingPrefix = "matching-prefix"
	WatchInRange        = "in-range"
)

// WatchStateStore is an interface for initialization and support state watcher
type WatchStateStore interface {
	Init(metadata Metadata) error
	Watch(req *WatchStateRequest) (<-chan *StateEvent, error)
}
