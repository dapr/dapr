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

package http

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	apierrors "github.com/dapr/dapr/pkg/api/errors"
	"github.com/dapr/dapr/pkg/api/http/endpoints"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

var endpointGroupSchedulerV1Alpha1 = &endpoints.EndpointGroup{
	Name:                 endpoints.EndpointGroupScheduler,
	Version:              endpoints.EndpointGroupVersion1alpha1,
	AppendSpanAttributes: nil,
}

func (a *api) constructSchedulerEndpoints() []endpoints.Endpoint {
	return []endpoints.Endpoint{
		{
			Methods: []string{http.MethodPost},
			Route:   "job/schedule/{name}",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupSchedulerV1Alpha1,
			Handler: a.onCreateScheduleHandler(),
			Settings: endpoints.EndpointSettings{
				Name: "ScheduleJob",
			},
		},
		{
			Methods: []string{http.MethodDelete},
			Route:   "job/{name}",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupSchedulerV1Alpha1,
			Handler: a.onDeleteJobHandler(),
			Settings: endpoints.EndpointSettings{
				Name: "DeleteJob",
			},
		},
		{
			Methods: []string{http.MethodGet},
			Route:   "job/{name}",
			Version: apiVersionV1alpha1,
			Group:   endpointGroupSchedulerV1Alpha1,
			Handler: a.onGetJobHandler(),
			Settings: endpoints.EndpointSettings{
				Name: "GetJob",
			},
		},
	}
}

func (a *api) onCreateScheduleHandler() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.ScheduleJobAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.ScheduleJobRequest, *runtimev1pb.ScheduleJobResponse]{
			InModifier: func(r *http.Request, in *runtimev1pb.ScheduleJobRequest) (*runtimev1pb.ScheduleJobRequest, error) {
				// Users should set the name in the url, and not in the url and body
				name := strings.TrimSpace(chi.URLParam(r, nameParam))
				if len(name) == 0 {
					apierrors.Empty("Job", map[string]string{"appID": a.universal.AppID()}, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.PostFixEmpty))
				}
				if in.GetJob().GetName() != "" {
					return nil, apierrors.SchedulerURLName(map[string]string{"appID": a.universal.AppID()})
				}

				if in.GetJob() == nil {
					in.Job = new(runtimev1pb.Job)
				}

				in.Job.Name = name

				return in, nil
			},
			OutModifier: func(out *runtimev1pb.ScheduleJobResponse) (any, error) {
				// Nullify the response so status code is 204
				return nil, nil // empty body
			},
		},
	)
}

func (a *api) onDeleteJobHandler() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.DeleteJobAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.DeleteJobRequest, *runtimev1pb.DeleteJobResponse]{
			SkipInputBody: true,
			InModifier: func(r *http.Request, in *runtimev1pb.DeleteJobRequest) (*runtimev1pb.DeleteJobRequest, error) {
				name := chi.URLParam(r, "name")
				in.Name = name
				return in, nil
			},
			OutModifier: func(out *runtimev1pb.DeleteJobResponse) (any, error) {
				// Nullify the response so status code is 204
				return nil, nil // empty body
			},
		},
	)
}

func (a *api) onGetJobHandler() http.HandlerFunc {
	return UniversalHTTPHandler(
		a.universal.GetJobAlpha1,
		UniversalHTTPHandlerOpts[*runtimev1pb.GetJobRequest, *runtimev1pb.GetJobResponse]{
			SkipInputBody: true,
			InModifier: func(r *http.Request, in *runtimev1pb.GetJobRequest) (*runtimev1pb.GetJobRequest, error) {
				name := chi.URLParam(r, "name")
				in.Name = name
				return in, nil
			},
			OutModifier: func(out *runtimev1pb.GetJobResponse) (any, error) {
				if out == nil || out.GetJob() == nil {
					return nil, nil // empty body
				}
				return out.GetJob(), nil // empty body
			},
		},
	)
}
