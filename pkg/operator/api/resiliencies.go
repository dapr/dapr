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

package api

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	resiliencyapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	"github.com/dapr/dapr/pkg/operator/api/authz"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

// GetResiliency returns a specified resiliency object.
func (a *apiServer) GetResiliency(ctx context.Context, in *operatorv1pb.GetResiliencyRequest) (*operatorv1pb.GetResiliencyResponse, error) {
	if _, err := authz.Request(ctx, in.GetNamespace()); err != nil {
		return nil, err
	}

	key := types.NamespacedName{Namespace: in.GetNamespace(), Name: in.GetName()}
	var resiliencyConfig resiliencyapi.Resiliency
	if err := a.Client.Get(ctx, key, &resiliencyConfig); err != nil {
		return nil, fmt.Errorf("error getting resiliency: %w", err)
	}
	b, err := json.Marshal(&resiliencyConfig)
	if err != nil {
		return nil, fmt.Errorf("error marshalling resiliency: %w", err)
	}
	return &operatorv1pb.GetResiliencyResponse{
		Resiliency: b,
	}, nil
}

// ListResiliency gets the list of applied resiliencies.
func (a *apiServer) ListResiliency(ctx context.Context, in *operatorv1pb.ListResiliencyRequest) (*operatorv1pb.ListResiliencyResponse, error) {
	if _, err := authz.Request(ctx, in.GetNamespace()); err != nil {
		return nil, err
	}

	resp := &operatorv1pb.ListResiliencyResponse{
		Resiliencies: [][]byte{},
	}

	var resiliencies resiliencyapi.ResiliencyList
	if err := a.Client.List(ctx, &resiliencies, &client.ListOptions{
		Namespace: in.GetNamespace(),
	}); err != nil {
		return nil, fmt.Errorf("error listing resiliencies: %w", err)
	}

	for _, item := range resiliencies.Items {
		b, err := json.Marshal(item)
		if err != nil {
			log.Warnf("Error unmarshalling resiliency: %s", err)
			continue
		}
		resp.Resiliencies = append(resp.GetResiliencies(), b)
	}

	return resp, nil
}
