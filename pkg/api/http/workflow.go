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
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/dapr/pkg/api/http/endpoints"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

var (
	endpointGroupWorkflowV1Alpha1 = &endpoints.EndpointGroup{
		Name:                 endpoints.EndpointGroupWorkflow,
		Version:              endpoints.EndpointGroupVersion1alpha1,
		AppendSpanAttributes: nil, // TODO
	}
	endpointGroupWorkflowV1Beta1 = &endpoints.EndpointGroup{
		Name:                 endpoints.EndpointGroupWorkflow,
		Version:              endpoints.EndpointGroupVersion1beta1,
		AppendSpanAttributes: nil, // TODO
	}
)

// Workflow Component: Component specified in yaml
// Workflow Name: Name of the workflow to run
// Instance ID: Identifier of the specific run
func (a *api) constructWorkflowEndpoints() []endpoints.Endpoint {
	return []endpoints.Endpoint{
		{
			Methods: []string{http.MethodGet},
			Route:   "workflows/{workflowComponent}/{instanceID}",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupWorkflowV1Alpha1,
			Handler: a.onGetWorkflowHandler(),
			Settings: endpoints.EndpointSettings{
				Name: "GetWorkflow",
			},
		},
		{
			Methods: []string{http.MethodGet},
			Route:   "workflows/{workflowComponent}/{instanceID}",
			Version: apiVersionV1beta1,
			Group:   endpointGroupWorkflowV1Beta1,
			Handler: a.onGetWorkflowHandler(),
			Settings: endpoints.EndpointSettings{
				Name: "GetWorkflow",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "workflows/{workflowComponent}/{instanceID}/raiseEvent/{eventName}",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupWorkflowV1Alpha1,
			Handler: a.onRaiseEventWorkflowHandler(),
			Settings: endpoints.EndpointSettings{
				Name: "RaiseEventWorkflow",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "workflows/{workflowComponent}/{instanceID}/raiseEvent/{eventName}",
			Version: apiVersionV1beta1,
			Group:   endpointGroupWorkflowV1Beta1,
			Handler: a.onRaiseEventWorkflowHandler(),
			Settings: endpoints.EndpointSettings{
				Name: "RaiseEventWorkflow",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "workflows/{workflowComponent}/{workflowName}/start",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupWorkflowV1Alpha1,
			Handler: a.onStartWorkflowHandler(),
			Settings: endpoints.EndpointSettings{
				Name: "StartWorkflow",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "workflows/{workflowComponent}/{workflowName}/start",
			Version: apiVersionV1beta1,
			Group:   endpointGroupWorkflowV1Beta1,
			Handler: a.onStartWorkflowHandler(),
			Settings: endpoints.EndpointSettings{
				Name: "StartWorkflow",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "workflows/{workflowComponent}/{instanceID}/pause",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupWorkflowV1Alpha1,
			Handler: a.onPauseWorkflowHandler(),
			Settings: endpoints.EndpointSettings{
				Name: "PauseWorkflow",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "workflows/{workflowComponent}/{instanceID}/pause",
			Version: apiVersionV1beta1,
			Group:   endpointGroupWorkflowV1Beta1,
			Handler: a.onPauseWorkflowHandler(),
			Settings: endpoints.EndpointSettings{
				Name: "PauseWorkflow",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "workflows/{workflowComponent}/{instanceID}/resume",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupWorkflowV1Alpha1,
			Handler: a.onResumeWorkflowHandler(),
			Settings: endpoints.EndpointSettings{
				Name: "ResumeWorkflow",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "workflows/{workflowComponent}/{instanceID}/resume",
			Version: apiVersionV1beta1,
			Group:   endpointGroupWorkflowV1Beta1,
			Handler: a.onResumeWorkflowHandler(),
			Settings: endpoints.EndpointSettings{
				Name: "ResumeWorkflow",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "workflows/{workflowComponent}/{instanceID}/terminate",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupWorkflowV1Alpha1,
			Handler: a.onTerminateWorkflowHandler(),
			Settings: endpoints.EndpointSettings{
				Name: "TerminateWorkflow",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "workflows/{workflowComponent}/{instanceID}/terminate",
			Version: apiVersionV1beta1,
			Group:   endpointGroupWorkflowV1Beta1,
			Handler: a.onTerminateWorkflowHandler(),
			Settings: endpoints.EndpointSettings{
				Name: "TerminateWorkflow",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "workflows/{workflowComponent}/{instanceID}/purge",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupWorkflowV1Alpha1,
			Handler: a.onPurgeWorkflowHandler(),
			Settings: endpoints.EndpointSettings{
				Name: "PurgeWorkflow",
			},
		},
		{
			Methods: []string{http.MethodPost},
			Route:   "workflows/{workflowComponent}/{instanceID}/purge",
			Version: apiVersionV1beta1,
			Group:   endpointGroupWorkflowV1Beta1,
			Handler: a.onPurgeWorkflowHandler(),
			Settings: endpoints.EndpointSettings{
				Name: "PurgeWorkflow",
			},
		},
	}
}

// Route:   "workflows/{workflowComponent}/{workflowName}/start?instanceID={instanceID}",
// Workflow Component: Component specified in yaml
// Workflow Name: Name of the workflow to run
// Instance ID: Identifier of the specific run
func (a *api) onStartWorkflowHandler() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.StartWorkflowBeta1,
		UniversalHTTPHandlerOpts[*runtimev1pb.StartWorkflowRequest, *runtimev1pb.StartWorkflowResponse]{
			// We pass the input body manually rather than parsing it using protojson
			SkipInputBody: true,
			InModifier: func(r *http.Request, in *runtimev1pb.StartWorkflowRequest) (*runtimev1pb.StartWorkflowRequest, error) {
				in.WorkflowName = chi.URLParam(r, workflowName)
				in.WorkflowComponent = chi.URLParam(r, workflowComponent)
				in.InstanceId = r.URL.Query().Get(instanceID)

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
		a.universal.GetWorkflowBeta1,
		UniversalHTTPHandlerOpts[*runtimev1pb.GetWorkflowRequest, *runtimev1pb.GetWorkflowResponse]{
			InModifier: workflowInModifier[*runtimev1pb.GetWorkflowRequest],
		})
}

// Route: POST "workflows/{workflowComponent}/{instanceID}/terminate"
func (a *api) onTerminateWorkflowHandler() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.TerminateWorkflowBeta1,
		UniversalHTTPHandlerOpts[*runtimev1pb.TerminateWorkflowRequest, *emptypb.Empty]{
			InModifier: func(r *http.Request, in *runtimev1pb.TerminateWorkflowRequest) (*runtimev1pb.TerminateWorkflowRequest, error) {
				in.SetWorkflowComponent(chi.URLParam(r, workflowComponent))
				in.SetInstanceId(chi.URLParam(r, instanceID))
				return in, nil
			},
			SuccessStatusCode: http.StatusAccepted,
		})
}

// Route: POST "workflows/{workflowComponent}/{instanceID}/events/{eventName}"
func (a *api) onRaiseEventWorkflowHandler() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.RaiseEventWorkflowBeta1,
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
		a.universal.PauseWorkflowBeta1,
		UniversalHTTPHandlerOpts[*runtimev1pb.PauseWorkflowRequest, *emptypb.Empty]{
			InModifier:        workflowInModifier[*runtimev1pb.PauseWorkflowRequest],
			SuccessStatusCode: http.StatusAccepted,
		})
}

// ROUTE: POST "workflows/{workflowComponent}/{instanceID}/resume"
func (a *api) onResumeWorkflowHandler() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.ResumeWorkflowBeta1,
		UniversalHTTPHandlerOpts[*runtimev1pb.ResumeWorkflowRequest, *emptypb.Empty]{
			InModifier:        workflowInModifier[*runtimev1pb.ResumeWorkflowRequest],
			SuccessStatusCode: http.StatusAccepted,
		})
}

func (a *api) onPurgeWorkflowHandler() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.PurgeWorkflowBeta1,
		UniversalHTTPHandlerOpts[*runtimev1pb.PurgeWorkflowRequest, *emptypb.Empty]{
			InModifier: func(r *http.Request, in *runtimev1pb.PurgeWorkflowRequest) (*runtimev1pb.PurgeWorkflowRequest, error) {
				in.SetWorkflowComponent(chi.URLParam(r, workflowComponent))
				in.SetInstanceId(chi.URLParam(r, instanceID))
				return in, nil
			},
			SuccessStatusCode: http.StatusAccepted,
		})
}

// Shared InModifier method for all universal handlers for workflows that adds the "WorkflowComponent" and "InstanceId" properties
func workflowInModifier[T runtimev1pb.WorkflowRequests](r *http.Request, in T) (T, error) {
	in.SetWorkflowComponent(chi.URLParam(r, workflowComponent))
	in.SetInstanceId(chi.URLParam(r, instanceID))
	return in, nil
}
