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

import (
	"errors"
	"fmt"

	"github.com/mitchellh/mapstructure"
	"golang.org/x/exp/maps"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/config"
)

// OperationType describes a CRUD operation performed against a state store.
type OperationType string

const (
	// Upsert is an update or create operation.
	Upsert OperationType = "upsert"
	// Delete is a delete operation.
	Delete OperationType = "delete"
)

// Options for the StateOperation method
type StateOperationOpts struct {
	Metadata    map[string]string
	ContentType *string

	// TODO: @joshvanl Remove in Dapr 1.12 when ActorStateTTL is finalized.
	StateTTLEnabled bool
}

// TransactionalRequest describes a set of stateful operations for a given actor that are performed in a transactional manner.
type TransactionalRequest struct {
	Operations []TransactionalOperation `json:"operations"`
	ActorType  string
	ActorID    string
}

// ActorKey returns the key of the actor for this request.
func (r TransactionalRequest) ActorKey() string {
	return r.ActorType + daprSeparator + r.ActorID
}

// TransactionalOperation is the request object for a state operation participating in a transaction.
type TransactionalOperation struct {
	Operation OperationType `json:"operation"`
	Request   any           `json:"request"`
}

// StateOperation returns the state.TransactionalStateOperation object.
func (t TransactionalOperation) StateOperation(baseKey string, opts StateOperationOpts) (op state.TransactionalStateOperation, err error) {
	if t.Request == nil {
		return op, errors.New("request data is empty")
	}
	if t.Operation == "" {
		return op, errors.New("operation is empty")
	}

	switch x := t.Request.(type) {
	case TransactionalUpsert:
		if t.Operation != Upsert {
			return op, fmt.Errorf("operation mismatched: expected %s but got %s", Upsert, t.Operation)
		}
		return x.StateOperation(baseKey, opts)
	case *TransactionalUpsert:
		if t.Operation != Upsert {
			return op, fmt.Errorf("operation mismatched: expected %s but got %s", Upsert, t.Operation)
		}
		return x.StateOperation(baseKey, opts)

	case TransactionalDelete:
		if t.Operation != Delete {
			return op, fmt.Errorf("operation mismatched: expected %s but got %s", Delete, t.Operation)
		}
		return x.StateOperation(baseKey, opts)
	case *TransactionalDelete:
		if t.Operation != Delete {
			return op, fmt.Errorf("operation mismatched: expected %s but got %s", Delete, t.Operation)
		}
		return x.StateOperation(baseKey, opts)

	default:
		// Fallback to using mapstructure to support other types
		switch t.Operation {
		case Upsert:
			var upsert TransactionalUpsert
			err := mapstructure.Decode(x, &upsert)
			if err != nil {
				return op, err
			}
			return upsert.StateOperation(baseKey, opts)

		case Delete:
			var delete TransactionalDelete
			err := mapstructure.Decode(x, &delete)
			if err != nil {
				return op, err
			}
			return delete.StateOperation(baseKey, opts)

		default:
			return op, fmt.Errorf("operation type %s not supported", t.Operation)
		}
	}
}

// TransactionalUpsert defines a key/value pair for an upsert operation.
type TransactionalUpsert struct {
	Key      string            `json:"key"`
	Value    any               `json:"value"`
	ETag     *string           `json:"etag,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// StateOperation returns the state.TransactionalStateOperation object.
func (t TransactionalUpsert) StateOperation(baseKey string, opts StateOperationOpts) (op state.TransactionalStateOperation, err error) {
	if t.Key == "" {
		return op, errors.New("missing key")
	}

	if len(t.Metadata) == 0 {
		t.Metadata = opts.Metadata
	} else {
		maps.Copy(t.Metadata, opts.Metadata)
	}

	// TODO: @joshvanl Remove in Dapr 1.13 when ActorStateTTL is out of preview.
	if !opts.StateTTLEnabled {
		if _, ok := t.Metadata["ttlInSeconds"]; ok {
			return op, fmt.Errorf("ttlInSeconds is not supported without the %q feature enabled", config.ActorStateTTL)
		}
	}

	return state.SetRequest{
		Key:         baseKey + t.Key,
		Value:       t.Value,
		Metadata:    t.Metadata,
		ETag:        t.ETag,
		ContentType: opts.ContentType,
	}, nil
}

// TransactionalDelete defined a delete operation.
type TransactionalDelete struct {
	Key  string  `json:"key"`
	ETag *string `json:"etag,omitempty"`
}

// StateOperation returns the state.TransactionalStateOperation object.
func (t TransactionalDelete) StateOperation(baseKey string, opts StateOperationOpts) (op state.TransactionalStateOperation, err error) {
	if t.Key == "" {
		return op, errors.New("missing key")
	}

	return state.DeleteRequest{
		Key:      baseKey + t.Key,
		Metadata: opts.Metadata,
		ETag:     t.ETag,
	}, nil
}
