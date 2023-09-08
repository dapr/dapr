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

package reconciler

import (
	"context"

	httpendpointsapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/authorizer"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/processor"
)

type endpoint struct {
	store *compstore.ComponentStore
	proc  *processor.Processor
	auth  *authorizer.Authorizer
	loader.Loader[httpendpointsapi.HTTPEndpoint]
}

//nolint:unused
func (e *endpoint) update(ctx context.Context, endpoint httpendpointsapi.HTTPEndpoint) {
	oldEndpoint, exists := e.store.GetHTTPEndpoint(endpoint.Name)
	_, _ = e.proc.Secret().ProcessResource(ctx, endpoint)

	if exists {
		if differ.AreSame(oldEndpoint, endpoint) {
			log.Info("HTTPEndpoint update skipped, no changes detected: %s", endpoint.LogName())
			return
		}

		e.store.DeleteHTTPEndpoint(endpoint.Name)
	}

	if !e.auth.IsObjectAuthorized(endpoint) {
		log.Warnf("Received unauthorized httpendpoint update, ignored. %s", endpoint.LogName())
		return
	}

	log.Infof("Adding HTTPEndpoint for processing: %s", endpoint.LogName())
	if e.proc.AddPendingEndpoint(ctx, endpoint) {
		log.Infof("HTTPEndpoint updated %s", endpoint.LogName())
	}
}

//nolint:unused
func (e *endpoint) delete(endpoint httpendpointsapi.HTTPEndpoint) {
	e.store.DeleteHTTPEndpoint(endpoint.Name)
}
