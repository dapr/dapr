// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

// OperationType describes a CRUD operation performed against a state store
type OperationType string

// Upsert is an update or create operation
const Upsert OperationType = "upsert"

// Delete is a delete operation
const Delete OperationType = "delete"

// TransactionalRequest describes a set of stateful operations for a given actor that are performed in a transactional manner
type TransactionalRequest struct {
	Operations []TransactionalOperation `json:"operations"`
	ActorType  string
	ActorID    string
}

type TransactionalOperation struct {
	Operation OperationType `json:"operation"`
	Request   interface{}   `json:"request"`
}

type TransactionalUpsert struct {
	Key   string      `json:"key"`
	Value interface{} `json:"value"`
}

type TransactionalDelete struct {
	Key string `json:"key"`
}
