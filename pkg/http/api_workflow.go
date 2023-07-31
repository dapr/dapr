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

package http

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// Workflow Component: Component specified in yaml
// Workflow Name: Name of the workflow to run
// Instance ID: Identifier of the specific run
func (a *api) constructWorkflowEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{http.MethodGet},
			Route:   "workflows/{workflowComponent}/{instanceID}",
			Version: apiVersionV1alpha1,
			Handler: a.onGetWorkflowHandler(),
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "workflows/{workflowComponent}/{instanceID}/raiseEvent/{eventName}",
			Version: apiVersionV1alpha1,
			Handler: a.onRaiseEventWorkflowHandler(),
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "workflows/{workflowComponent}/{workflowName}/start",
			Version: apiVersionV1alpha1,
			Handler: a.onStartWorkflowHandler(),
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "workflows/{workflowComponent}/{instanceID}/pause",
			Version: apiVersionV1alpha1,
			Handler: a.onPauseWorkflowHandler(),
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "workflows/{workflowComponent}/{instanceID}/resume",
			Version: apiVersionV1alpha1,
			Handler: a.onResumeWorkflowHandler(),
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "workflows/{workflowComponent}/{instanceID}/terminate",
			Version: apiVersionV1alpha1,
			Handler: a.onTerminateWorkflowHandler(),
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "workflows/{workflowComponent}/{instanceID}/purge",
			Version: apiVersionV1alpha1,
			Handler: a.onPurgeWorkflowHandler(),
		},
	}
}

// Route:   "workflows/{workflowComponent}/{workflowName}/start?instanceID={instanceID}",
// Workflow Component: Component specified in yaml
// Workflow Name: Name of the workflow to run
// Instance ID: Identifier of the specific run
func (a *api) onStartWorkflowHandler() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.StartWorkflowAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.StartWorkflowRequest, *runtimev1pb.StartWorkflowResponse]{
			// We pass the input body manually rather than parsing it using protojson
			SkipInputBody: true,
			InModifier: func(r *http.Request, in *runtimev1pb.StartWorkflowRequest) (*runtimev1pb.StartWorkflowRequest, error) {
				in.WorkflowName = chi.URLParam(r, workflowName)
				in.WorkflowComponent = chi.URLParam(r, workflowComponent)

				// The instance ID is optional. If not specified, we generate a random one.
				in.InstanceId = r.URL.Query().Get(instanceID)
				if in.InstanceId == "" {
					randomID, err := uuid.NewRandom()
					if err != nil {
						return nil, err
					}
					in.InstanceId = randomID.String()
				}

				// We accept the HTTP request body as the input to the workflow
				// without making any assumptions about its format.
				var err error
				in.Input, err = io.ReadAll(r.Body)
				if err != nil {
					return nil, messages.ErrBodyRead.WithFormat(err)
				}
				return in, nil
			},
			SuccessStatusCode: http.StatusAccepted,
		})
}

// Route: POST "workflows/{workflowComponent}/{instanceID}"
func (a *api) onGetWorkflowHandler() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.GetWorkflowAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.GetWorkflowRequest, *runtimev1pb.GetWorkflowResponse]{
			InModifier: workflowInModifier[*runtimev1pb.GetWorkflowRequest],
		})
}

// Route: POST "workflows/{workflowComponent}/{instanceID}/terminate"
func (a *api) onTerminateWorkflowHandler() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.TerminateWorkflowAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.TerminateWorkflowRequest, *emptypb.Empty]{
			InModifier:        workflowInModifier[*runtimev1pb.TerminateWorkflowRequest],
			SuccessStatusCode: http.StatusAccepted,
		})
}

// Route: POST "workflows/{workflowComponent}/{instanceID}/events/{eventName}"
func (a *api) onRaiseEventWorkflowHandler() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.RaiseEventWorkflowAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.RaiseEventWorkflowRequest, *emptypb.Empty]{
			// We pass the input body manually rather than parsing it using protojson
			SkipInputBody: true,
			InModifier: func(r *http.Request, in *runtimev1pb.RaiseEventWorkflowRequest) (*runtimev1pb.RaiseEventWorkflowRequest, error) {
				in.InstanceId = chi.URLParam(r, instanceID)
				in.WorkflowComponent = chi.URLParam(r, workflowComponent)
				in.EventName = chi.URLParam(r, eventName)

				// We accept the HTTP request body as the payload of the workflow event
				// without making any assumptions about its format.
				var err error
				in.EventData, err = io.ReadAll(r.Body)
				if err != nil {
					return nil, messages.ErrBodyRead.WithFormat(err)
				}
				return in, nil
			},
			SuccessStatusCode: http.StatusAccepted,
		})
}

// ROUTE: POST "workflows/{workflowComponent}/{instanceID}/pause"
func (a *api) onPauseWorkflowHandler() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.PauseWorkflowAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.PauseWorkflowRequest, *emptypb.Empty]{
			InModifier:        workflowInModifier[*runtimev1pb.PauseWorkflowRequest],
			SuccessStatusCode: http.StatusAccepted,
		})
}

// ROUTE: POST "workflows/{workflowComponent}/{instanceID}/resume"
func (a *api) onResumeWorkflowHandler() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.ResumeWorkflowAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.ResumeWorkflowRequest, *emptypb.Empty]{
			InModifier:        workflowInModifier[*runtimev1pb.ResumeWorkflowRequest],
			SuccessStatusCode: http.StatusAccepted,
		})
}

func (a *api) onPurgeWorkflowHandler() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.PurgeWorkflowAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.PurgeWorkflowRequest, *emptypb.Empty]{
			InModifier:        workflowInModifier[*runtimev1pb.PurgeWorkflowRequest],
			SuccessStatusCode: http.StatusAccepted,
		})
}

// Shared InModifier method for all universal handlers for workflows that adds the "WorkflowComponent" and "InstanceId" properties
func workflowInModifier[T runtimev1pb.WorkflowRequests](r *http.Request, in T) (T, error) {
	in.SetWorkflowComponent(chi.URLParam(r, workflowComponent))
	in.SetInstanceId(chi.URLParam(r, instanceID))
	return in, nil
}
