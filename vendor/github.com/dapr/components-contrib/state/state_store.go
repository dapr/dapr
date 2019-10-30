// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package state

// StateStore is an interface to perform operations on store
// nolint:golint
type StateStore interface {
	Init(metadata Metadata) error
	Delete(req *DeleteRequest) error
	BulkDelete(req []DeleteRequest) error
	Get(req *GetRequest) (*GetResponse, error)
	Set(req *SetRequest) error
	BulkSet(req []SetRequest) error
}
