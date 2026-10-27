/*
Copyright 2026 The Dapr Authors
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

// Package marker is the factory of an actor type registered only so that
// the placement tables show which hosts register it. Its actors hold no
// state and serve no calls.
package marker

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

type factory struct {
	actorType string
}

// New returns the factory of the marker actor type actorType. Nothing is
// expected to call its actors: a call fails with Unimplemented, and the
// factory keeps no actors, so there are none to halt.
func New(actorType string) targets.Factory {
	return factory{actorType: actorType}
}

func (f factory) GetOrCreate(actorID string) targets.Interface {
	return actor{actorType: f.actorType, id: actorID}
}

func (factory) Exists(string) bool            { return false }
func (factory) HaltAll(context.Context) error { return nil }
func (factory) Len() int                      { return 0 }

func (factory) HaltNonHosted(context.Context, func(*api.LookupActorRequest) bool) error {
	return nil
}

type actor struct {
	actorType string
	id        string
}

func (a actor) unimplemented() error {
	return status.Errorf(codes.Unimplemented, "actor '%s' only marks the hosts which register its type, and serves no calls", a.Key())
}

func (a actor) Key() string  { return a.actorType + api.DaprSeparator + a.id }
func (a actor) Type() string { return a.actorType }
func (a actor) ID() string   { return a.id }

func (a actor) InvokeMethod(context.Context, *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	return nil, a.unimplemented()
}

func (a actor) InvokeReminder(context.Context, *api.Reminder) error {
	return a.unimplemented()
}

func (a actor) InvokeTimer(context.Context, *api.Reminder) error {
	return a.unimplemented()
}

func (a actor) InvokeStream(context.Context, *internalv1pb.InternalInvokeRequest, func(*internalv1pb.InternalInvokeResponse) (bool, error)) error {
	return a.unimplemented()
}

func (actor) Deactivate(context.Context) error { return nil }
