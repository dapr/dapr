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

package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"strings"

	serve "github.com/dapr/dapr/tests/apps/actorload/cmd/stateactor/service"
	cl "github.com/dapr/dapr/tests/apps/actorload/pkg/actor/client"
	httpClient "github.com/dapr/dapr/tests/apps/actorload/pkg/actor/client/http"
	rt "github.com/dapr/dapr/tests/apps/actorload/pkg/actor/runtime"
)

const (
	// actorType is Actor Type Name for test.
	actorType = "StateActor"
	// actorStateName is Actor State name.
	actorStateName = "state"
	daprAppPort    = 3000
)

var (
	actors  = flag.String("actors", actorType, "Actor types array separated by comma. e.g. StateActor,SaveActor")
	appPort = flag.Int("p", daprAppPort, "StateActor service app port.")
)

type stateActor struct {
	actorClient cl.ActorClient
}

func newStateActor() *stateActor {
	return &stateActor{
		actorClient: httpClient.NewClient(),
	}
}

func (s *stateActor) setActorState(actorType, actorID string, data []byte, metadata map[string]string) ([]byte, error) {
	upsertReq := httpClient.TransactionalStateOperation{
		Operation: "upsert",
		Request: httpClient.TransactionalRequest{
			Key:   actorStateName,
			Value: string(data),
		},
	}

	operations := []httpClient.TransactionalStateOperation{upsertReq}
	serialized, err := json.Marshal(operations)
	if err != nil {
		return nil, err
	}

	if err := s.actorClient.SaveStateTransactionally(actorType, actorID, serialized); err != nil {
		return nil, err
	}

	return []byte(""), nil
}

func (s *stateActor) nopMethod(actorType, actorID string, data []byte, metadata map[string]string) ([]byte, error) {
	return []byte("nop"), nil
}

func (s *stateActor) getActorState(actorType, actorID string, data []byte, metadata map[string]string) ([]byte, error) {
	data, err := s.actorClient.GetState(actorType, actorID, actorStateName)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func (s *stateActor) onActivated(actorType, actorID string) error {
	hostname, _ := os.Hostname()
	log.Printf("%s.%s, %s, %s", actorType, actorID, hostname, "Activated")
	return nil
}

func (s *stateActor) onDeactivated(actorType, actorID string) error {
	hostname, _ := os.Hostname()
	log.Printf("%s.%s, %s, %s", actorType, actorID, hostname, "Deactivated")
	return nil
}

func main() {
	flag.Parse()

	actorTypes := strings.Split(*actors, ",")

	service := serve.NewActorService(*appPort, &rt.DaprConfig{
		Entities:                actorTypes,
		ActorIdleTimeout:        "5m",
		ActorScanInterval:       "10s",
		DrainOngoingCallTimeout: "10s",
		DrainRebalancedActors:   true,
	})

	actor := newStateActor()

	service.SetActivationHandler(actor.onActivated)
	service.SetDeactivationHandler(actor.onDeactivated)
	service.AddActorMethod("getActorState", actor.getActorState)
	service.AddActorMethod("setActorState", actor.setActorState)
	service.AddActorMethod("nop", actor.nopMethod)

	service.StartServer()
}
