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

package wasm

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/taction/daprwasmactor/sdk"

	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/apphealth"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
)

const (
	// HTTPStatusCode is an dapr http channel status code.
	HTTPStatusCode    = "http.status_code"
	httpScheme        = "http"
	httpsScheme       = "https"
	appConfigEndpoint = "dapr/config"
)

var log = logger.NewLogger("dapr.channel.wasm")

// Channel is an WASM implementation of an AppChannel.
type Channel struct {
	pools          sync.Pool
	maxConcurrency int
	actor          actors.Actors
	module         WasmModule
	wasmPath       string
}

type WasmModule interface {
	Instantiate(ctx context.Context) (WasmInstance, error)
	Check() error // Check if the module is valid
}

// WasmInstance is an instantiated WasmModule
type WasmInstance interface {
	// ActorCalled calls wasm actor .
	ActorCalled(ctx context.Context, req *sdk.InvokeActorRequest) (data *sdk.InvokeActorResponse)

	// Close releases resources from this instance, returning the first error encountered.
	// Note: This should be called before calling Module.Close.
	Close(context.Context) error
}

// CreateWASMChannel creates an WASM AppChannel
//
//nolint:gosec
func CreateWASMChannel(maxConcurrency int, wasmPath string) (channel.AppChannel, error) {
	c := &Channel{maxConcurrency: 10, wasmPath: wasmPath}
	if maxConcurrency > 0 {
		c.maxConcurrency = maxConcurrency
	}

	return c, nil
}

func (h *Channel) initWithWasm(wasmPath string) error {
	code, err := os.ReadFile(wasmPath)
	if err != nil {
		return err
	}
	h.module, err = NewModule(context.TODO(), code, h.actor)
	if err != nil {
		return err
	}
	return nil
}

// GetBaseAddress returns the application base address.
func (h *Channel) GetBaseAddress() string {
	return ""
}

func (h *Channel) InitWithActor(actor actors.Actors) error {
	h.actor = actor
	return h.initWithWasm(h.wasmPath)
}

// GetAppConfig gets application config from user application
func (h *Channel) GetAppConfig() (*config.ApplicationConfig, error) {
	// todo this is a hook for now, app config needs to be set by other means
	return &config.ApplicationConfig{
		Entities: []string{"actor"},
	}, nil
}

// InvokeMethod invokes local wasm actor
func (h *Channel) InvokeMethod(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	var rsp *invokev1.InvokeMethodResponse
	var err error
	actorType := req.Actor().ActorType
	actorID := req.Actor().ActorId
	contentType, body := req.RawData()
	method := req.Message().GetMethod()
	log.Infof("actor %s is called", actorType)
	prefixLen := len(fmt.Sprintf("actors/%s/%s/method/", actorType, actorID))
	originMethod := method[prefixLen:]

	ins, err := h.getOrCreateActor(actorType)
	if err != nil {
		return nil, err
	}
	defer func() {
		h.pools.Put(ins)
	}()
	res := ins.ActorCalled(ctx, &sdk.InvokeActorRequest{
		ActorType:   actorType,
		ActorID:     actorID,
		Method:      originMethod,
		Data:        body,
		ContentType: contentType,
	})
	if err != nil {
		return nil, err
	}

	rsp = invokev1.NewInvokeMethodResponse(res.StatusCode, "", nil)
	rsp.WithRawData(res.Data, res.ContentType)
	return rsp, err
}

func (h *Channel) getOrCreateActor(actorType string) (WasmInstance, error) {
	// todo fix type
	i := h.pools.Get()
	if i != nil {
		return i.(WasmInstance), nil
	}
	return h.module.Instantiate(context.TODO())
}

// HealthProbe performs a health probe.
func (h *Channel) HealthProbe(ctx context.Context) (bool, error) {
	return true, nil
}

// SetAppHealth sets the apphealth.AppHealth object.
func (h *Channel) SetAppHealth(ah *apphealth.AppHealth) {
}
