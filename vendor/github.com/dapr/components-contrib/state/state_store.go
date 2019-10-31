// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package state

// Store is an interface to perform operations on store
type Store interface {
	Init(metadata Metadata) error
	Delete(req *DeleteRequest) error
	BulkDelete(req []DeleteRequest) error
	Get(req *GetRequest) (*GetResponse, error)
	Set(req *SetRequest) error
	BulkSet(req []SetRequest) error
}
