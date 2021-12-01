/*
Copyright 2021 The Dapr Authors
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

package actors

// OperationType describes a CRUD operation performed against a state store.
type OperationType string

// Upsert is an update or create operation.
const Upsert OperationType = "upsert"

// Delete is a delete operation.
const Delete OperationType = "delete"

// TransactionalRequest describes a set of stateful operations for a given actor that are performed in a transactional manner.
type TransactionalRequest struct {
	Operations []TransactionalOperation `json:"operations"`
	ActorType  string
	ActorID    string
}

// TransactionalOperation is the request object for a state operation participating in a transaction.
type TransactionalOperation struct {
	Operation OperationType `json:"operation"`
	Request   interface{}   `json:"request"`
}

// TransactionalUpsert defines a key/value pair for an upsert operation.
type TransactionalUpsert struct {
	Key   string      `json:"key"`
	Value interface{} `json:"value"`
}

// TransactionalDelete defined a delete operation.
type TransactionalDelete struct {
	Key string `json:"key"`
}
