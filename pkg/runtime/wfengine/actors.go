/*
Copyright 2022 The Dapr Authors
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
package wfengine

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"strings"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/apphealth"
	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
)

const (
	WorkflowActorType = "dapr.wfengine.workflow"
	ActivityActorType = "dapr.wfengine.activity"
)

// InternalActor represents the interface for invoking an "internal" actor (one which is built into daprd directly).
type internalActor interface {
	InvokeMethod(ctx context.Context, actorID string, methodName string, request []byte) (interface{}, error)
	DeactivateActor(ctx context.Context, actorID string) error
	InvokeReminder(ctx context.Context, actorID string, reminderName string, params []byte) error
	InvokeTimer(ctx context.Context, actorID string, timerName string, params []byte) error
}

type internalActorChannel struct {
	actors map[string]internalActor
}

func newInternalActorChannel() *internalActorChannel {
	return &internalActorChannel{}
}

func (c *internalActorChannel) InitializeActors(actorRuntime actors.Actors, be *actorBackend) {
	c.actors = make(map[string]internalActor)
	c.actors[WorkflowActorType] = NewWorkflowActor(actorRuntime, be)
	c.actors[ActivityActorType] = nil // TODO
}

// GetAppConfig implements channel.AppChannel
func (internalActorChannel) GetAppConfig() (*config.ApplicationConfig, error) {
	config := &config.ApplicationConfig{
		Entities: []string{WorkflowActorType, ActivityActorType},
	}
	return config, nil
}

// GetBaseAddress implements channel.AppChannel
func (internalActorChannel) GetBaseAddress() string {
	return ""
}

// HealthProbe implements channel.AppChannel
func (internalActorChannel) HealthProbe(ctx context.Context) (bool, error) {
	return true, nil
}

// InvokeMethod implements channel.AppChannel
func (c *internalActorChannel) InvokeMethod(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	actorType := req.Actor().GetActorType()
	actor, ok := c.actors[actorType]
	if !ok {
		return nil, fmt.Errorf("internal actor type '%s' not recognized.", actorType)
	}

	// The actor runtime formats method names as URLs in the form actors/{type}/{id}/method/{methodName}
	methodUrl := req.Message().GetMethod()
	methodStartIndex := strings.Index(methodUrl, "/method/")
	if methodStartIndex < 0 {
		return nil, fmt.Errorf("unexpected method format: %s", methodUrl)
	}
	methodName := methodUrl[methodStartIndex+len("/method/"):]

	requestData := req.Message().GetData().GetValue()
	verb := req.Message().GetHttpExtension().GetVerb()
	actorID := req.Actor().GetActorId()

	// Call the appropriate method based on the method name and verb.
	var result interface{} = nil
	var err error
	if strings.HasPrefix(methodName, "remind/") {
		reminderName := strings.TrimPrefix(methodName, "remind/")
		err = actor.InvokeReminder(ctx, actorID, reminderName, requestData)
	} else if strings.HasPrefix(methodName, "timer/") {
		timerName := strings.TrimPrefix(methodName, "timer/")
		err = actor.InvokeTimer(ctx, actorID, timerName, requestData)
	} else if verb == commonv1pb.HTTPExtension_DELETE { //nolint:nosnakecase
		err = actor.DeactivateActor(ctx, actorID)
	} else {
		result, err = actor.InvokeMethod(ctx, actorID, methodName, requestData)
	}

	if err != nil {
		return nil, err
	}

	// results for internal actors are serialized using binary Go serialization (https://go.dev/blog/gob)
	var resultData []byte
	if result != nil {
		var resultBuffer bytes.Buffer
		enc := gob.NewEncoder(&resultBuffer)
		if err := enc.Encode(result); err != nil {
			return nil, err
		}
		resultData = resultBuffer.Bytes()
	}
	res := invokev1.NewInvokeMethodResponse(200, "OK", nil).
		WithRawData(resultData, invokev1.JSONContentType)
	return res, nil
}

// SetAppHealth implements channel.AppChannel
func (internalActorChannel) SetAppHealth(ah *apphealth.AppHealth) {
	// no-op
}
