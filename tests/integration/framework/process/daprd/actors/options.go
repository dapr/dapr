/*
Copyright 2024 The Dapr Authors
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
	"net/http"
	"time"

	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
)

type Option func(*options)

type options struct {
	db    *sqlite.SQLite
	types []string

	placement               *placement.Placement
	scheduler               *scheduler.Scheduler
	daprdConfigs            []string
	actorTypeHandlers       map[string]http.HandlerFunc
	handlers                map[string]http.HandlerFunc
	reentry                 *bool
	reentryMaxDepth         *uint32
	actorIdleTimeout        *time.Duration
	drainOngoingCallTimeout *time.Duration
	drainRebalancedActors   *bool
	entityConfig            []entityConfig
	resources               []string
	maxBodySize             *string
}

func WithDB(db *sqlite.SQLite) Option {
	return func(o *options) {
		o.db = db
	}
}

func WithActorTypes(types ...string) Option {
	return func(o *options) {
		for _, atype := range types {
			o.types = append(o.types, `"`+atype+`"`)
		}
	}
}

func WithPlacement(placement *placement.Placement) Option {
	return func(o *options) {
		o.placement = placement
	}
}

func WithScheduler(scheduler *scheduler.Scheduler) Option {
	return func(o *options) {
		o.scheduler = scheduler
	}
}

func WithActorTypeHandler(actorType string, handler http.HandlerFunc) Option {
	return func(o *options) {
		if o.actorTypeHandlers == nil {
			o.actorTypeHandlers = make(map[string]http.HandlerFunc)
		}
		o.actorTypeHandlers[actorType] = handler
	}
}

func WithHandler(pattern string, handler http.HandlerFunc) Option {
	return func(o *options) {
		if o.handlers == nil {
			o.handlers = make(map[string]http.HandlerFunc)
		}
		o.handlers[pattern] = handler
	}
}

func WithPeerActor(actor *Actors) Option {
	return func(o *options) {
		WithDB(actor.DB())(o)
		WithPlacement(actor.Placement())(o)
		WithScheduler(actor.Scheduler())(o)
	}
}

func WithReentry(enabled bool) Option {
	return func(o *options) {
		o.reentry = &enabled
	}
}

func WithReentryMaxDepth(maxDepth uint32) Option {
	return func(o *options) {
		o.reentryMaxDepth = &maxDepth
	}
}

func WithActorIdleTimeout(timeout time.Duration) Option {
	return func(o *options) {
		o.actorIdleTimeout = &timeout
	}
}

func WithDrainOngoingCallTimeout(timeout time.Duration) Option {
	return func(o *options) {
		o.drainOngoingCallTimeout = &timeout
	}
}

func WithDrainRebalancedActors(drain bool) Option {
	return func(o *options) {
		o.drainRebalancedActors = &drain
	}
}

func WithEntityConfig(opts ...EntityConfig) Option {
	return func(o *options) {
		var e entityConfig
		for _, opt := range opts {
			opt(&e)
		}
		o.entityConfig = append(o.entityConfig, e)
	}
}

func WithResources(resources ...string) Option {
	return func(o *options) {
		o.resources = append(o.resources, resources...)
	}
}

func WithMaxBodySize(size string) Option {
	return func(o *options) {
		o.maxBodySize = &size
	}
}
