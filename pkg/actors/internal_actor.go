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
	"strings"

	"github.com/dapr/dapr/pkg/apphealth"
	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
)

const InternalActorTypePrefix = "dapr.internal."

// InternalActor represents the interface for invoking an "internal" actor (one which is built into daprd directly).
type InternalActor interface {
	SetActorRuntime(actorsRuntime Actors)
	InvokeMethod(ctx context.Context, actorID string, methodName string, data []byte) (any, error)
	DeactivateActor(ctx context.Context, actorID string) error
	// InvokeReminder invokes reminder logic for an internal actor.
	// Note that the DecodeInternalActorReminderData function should be used to decode the [data] parameter.
	InvokeReminder(ctx context.Context, actorID string, reminderName string, data []byte, dueTime string, period string) error
	InvokeTimer(ctx context.Context, actorID string, timerName string, params []byte) error
}

type internalActorChannel struct {
	actors map[string]InternalActor
}

func newInternalActorChannel() *internalActorChannel {
	return &internalActorChannel{
		actors: make(map[string]InternalActor),
	}
}

func (c *internalActorChannel) AddInternalActor(actorType string, actorImpl InternalActor) error {
	// use internal type name prefixes to avoid conflicting with externally defined actor types
	internalTypeName := actorType
	if !strings.HasPrefix(actorType, InternalActorTypePrefix) {
		internalTypeName = InternalActorTypePrefix + actorType
	}
	if _, exists := c.actors[internalTypeName]; exists {
		return fmt.Errorf("internal actor named '%s' already exists", actorType)
	}
	c.actors[internalTypeName] = actorImpl
	return nil
}

// Contains returns true if this channel invokes actorType or false if it doesn't.
func (c *internalActorChannel) Contains(actorType string) bool {
	_, exists := c.actors[actorType]
	return exists
}

// GetAppConfig implements channel.AppChannel
func (c *internalActorChannel) GetAppConfig() (*config.ApplicationConfig, error) {
	actorTypes := make([]string, 0, len(c.actors))
	for actorType := range c.actors {
		actorTypes = append(actorTypes, actorType)
	}
	config := &config.ApplicationConfig{
		Entities: actorTypes,
	}
	return config, nil
}

// HealthProbe implements channel.AppChannel
func (internalActorChannel) HealthProbe(ctx context.Context) (bool, error) {
	return true, nil
}

// InvokeMethod implements channel.AppChannel
func (c *internalActorChannel) InvokeMethod(ctx context.Context, req *invokev1.InvokeMethodRequest, _ string) (*invokev1.InvokeMethodResponse, error) {
	actorType := req.Actor().GetActorType()
	actor, ok := c.actors[actorType]
	if !ok {
		return nil, fmt.Errorf("internal actor type '%s' not recognized", actorType)
	}

	var result interface{} = nil
	var err error

	verb := req.Message().GetHttpExtension().GetVerb()
	actorID := req.Actor().GetActorId()

	if verb == commonv1pb.HTTPExtension_DELETE { //nolint:nosnakecase
		err = actor.DeactivateActor(ctx, actorID)
	} else {
		// The actor runtime formats method names as URLs in the form actors/{type}/{id}/method/{methodName}
		methodURL := req.Message().GetMethod()
		methodStartIndex := strings.Index(methodURL, "/method/")
		if methodStartIndex < 0 {
			return nil, fmt.Errorf("unexpected method format: %s", methodURL)
		}
		methodName := methodURL[methodStartIndex+len("/method/"):]

		// Check for well-known method names; otherwise, just call InvokeMethod on the internal actor.
		if strings.HasPrefix(methodName, "remind/") {
			reminderName := strings.TrimPrefix(methodName, "remind/")
			reminderInfo, ok := req.GetDataObject().(*ReminderResponse)
			if !ok {
				return nil, fmt.Errorf("unexpected type for reminder object: %T", req.GetDataObject())
			}

			// Reminder payloads are always saved as JSON
			dataBytes, ok := reminderInfo.Data.(json.RawMessage)
			if !ok {
				return nil, fmt.Errorf("unexpected type for data object: %T", reminderInfo.Data)
			}
			err = actor.InvokeReminder(ctx, actorID, reminderName, dataBytes, reminderInfo.DueTime, reminderInfo.Period)
		} else {
			var requestData []byte
			requestData, err = req.RawDataFull()
			if err != nil {
				return nil, err
			}

			if strings.HasPrefix(methodName, "timer/") {
				timerName := strings.TrimPrefix(methodName, "timer/")
				err = actor.InvokeTimer(ctx, actorID, timerName, requestData)
			} else {
				result, err = actor.InvokeMethod(ctx, actorID, methodName, requestData)
			}
		}
	}

	if err != nil {
		return nil, err
	}

	resultData, err := EncodeInternalActorData(result)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the internal actor response: %w", err)
	}
	res := invokev1.NewInvokeMethodResponse(200, "OK", nil).
		WithRawDataBytes(resultData).
		WithContentType(invokev1.OctetStreamContentType)
	return res, nil
}

// SetAppHealth implements channel.AppChannel
func (internalActorChannel) SetAppHealth(ah *apphealth.AppHealth) {
	// no-op
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
func DecodeInternalActorData(data []byte, e any) error {
	// Decode the data using encoding/gob (https://go.dev/blog/gob)
	buffer := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buffer)
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
