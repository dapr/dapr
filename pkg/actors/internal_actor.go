/*
Copyright 2023 The Dapr Authors
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
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"io"
)

const InternalActorTypePrefix = "dapr.internal."

// InternalActor represents the interface for invoking an "internal" actor (one which is built into daprd directly).
type InternalActor interface {
	SetActorRuntime(actorsRuntime Actors)
	InvokeMethod(ctx context.Context, actorID string, methodName string, data []byte, metadata map[string][]string) (any, error)
	DeactivateActor(ctx context.Context, actorID string) error
	InvokeReminder(ctx context.Context, actorID string, reminderName string, data []byte, dueTime string, period string) error
	InvokeTimer(ctx context.Context, actorID string, timerName string, data []byte, dueTime string, period string, callback string) error
}

// EncodeInternalActorData encodes result using the encoding/gob format.
func EncodeInternalActorData(result any) ([]byte, error) {
	var data []byte
	if result != nil {
		var resultBuffer bytes.Buffer
		enc := gob.NewEncoder(&resultBuffer)
		if err := enc.Encode(result); err != nil {
			return nil, err
		}
		data = resultBuffer.Bytes()
	}
	return data, nil
}

// DecodeInternalActorData decodes encoding/gob data and stores the result in e.
func DecodeInternalActorData(data io.Reader, e any) error {
	// Decode the data using encoding/gob (https://go.dev/blog/gob)
	dec := gob.NewDecoder(data)
	if err := dec.Decode(e); err != nil {
		return err
	}
	return nil
}

// DecodeInternalActorReminderData decodes internal actor reminder data payloads and stores the result in e.
func DecodeInternalActorReminderData(data []byte, e any) error {
	if err := json.Unmarshal(data, e); err != nil {
		return fmt.Errorf("unrecognized internal actor reminder payload: %w", err)
	}
	return nil
}
