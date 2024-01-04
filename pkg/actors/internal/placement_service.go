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

package internal

import (
	"context"
	"io"
	"time"
)

// HaltActorFn is the signature of the function invoked when the placement service requires an actor to be deactivated.
type HaltActorFn = func(actorType string, actorID string) error

// HaltAllActorsFn is the signature of the function invoked when the placement service requires all actors to be deactivated.
type HaltAllActorsFn = func() error

// PlacementService allows for interacting with the actor placement service.
//
//nolint:interfacebloat
type PlacementService interface {
	io.Closer

	Start(context.Context) error
	WaitUntilReady(ctx context.Context) error
	LookupActor(ctx context.Context, req LookupActorRequest) (LookupActorResponse, error)
	AddHostedActorType(actorType string, idleTimeout time.Duration) error
	ReportActorDeactivation(ctx context.Context, actorType, actorID string) error

	SetHaltActorFns(haltFn HaltActorFn, haltAllFn HaltAllActorsFn)
	SetOnAPILevelUpdate(fn func(apiLevel uint32))
	SetOnTableUpdateFn(fn func())

	// PlacementHealthy returns true if the placement service is healthy.
	PlacementHealthy() bool
	// StatusMessage returns a custom status message.
	StatusMessage() string
}

// LookupActorRequest is the request for LookupActor.
type LookupActorRequest struct {
	ActorType string
	ActorID   string
}

// ActorKey returns the key for the actor, which is "type/id".
func (lar LookupActorRequest) ActorKey() string {
	return lar.ActorType + "/" + lar.ActorID
}

// LookupActorResponse is the response from LookupActor.
type LookupActorResponse struct {
	Address string
	AppID   string
}
