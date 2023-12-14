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
	"sync/atomic"

	kclock "k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/security"
)

// AppHealthFn is a function that returns a channel which is notified of changes in app health status.
type AppHealthFn func(ctx context.Context) <-chan bool

// ActorsProviderOptions contains the options for providers of actors services.
type ActorsProviderOptions struct {
	Config     Config
	Security   security.Handler
	Resiliency resiliency.Provider

	AppHealthFn AppHealthFn

	// Pointer to the API level object
	APILevel *atomic.Uint32

	Clock kclock.WithTicker
}
