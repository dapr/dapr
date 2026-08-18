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
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	httpendpointsapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/operator/api/authz"
	loopsclient "github.com/dapr/dapr/pkg/operator/api/loops/client"
	"github.com/dapr/dapr/pkg/operator/api/loops/sender"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

func processHTTPEndpointSecrets(ctx context.Context, endpoint *httpendpointsapi.HTTPEndpoint, namespace string, kubeClient client.Client) error {
	for i, header := range endpoint.Spec.Headers {
		if pairNeedsSecretExtraction(header.SecretKeyRef, endpoint.Auth) {
			v, err := getSecret(ctx, header.SecretKeyRef.Name, namespace, header.SecretKeyRef, kubeClient)
			if err != nil {
				return err
			}

			endpoint.Spec.Headers[i].Value = v
		}
	}

	if endpoint.HasTLSClientCertSecret() && pairNeedsSecretExtraction(*endpoint.Spec.ClientTLS.Certificate.SecretKeyRef, endpoint.Auth) {
		v, err := getSecret(ctx, endpoint.Spec.ClientTLS.Certificate.SecretKeyRef.Name, namespace, *endpoint.Spec.ClientTLS.Certificate.SecretKeyRef, kubeClient)
		if err != nil {
			return err
		}

		endpoint.Spec.ClientTLS.Certificate.Value = &v
	}

	if endpoint.HasTLSPrivateKeySecret() && pairNeedsSecretExtraction(*endpoint.Spec.ClientTLS.PrivateKey.SecretKeyRef, endpoint.Auth) {
		v, err := getSecret(ctx, endpoint.Spec.ClientTLS.PrivateKey.SecretKeyRef.Name, namespace, *endpoint.Spec.ClientTLS.PrivateKey.SecretKeyRef, kubeClient)
		if err != nil {
			return err
		}

		endpoint.Spec.ClientTLS.PrivateKey.Value = &v
	}

	if endpoint.HasTLSRootCASecret() && pairNeedsSecretExtraction(*endpoint.Spec.ClientTLS.RootCA.SecretKeyRef, endpoint.Auth) {
		v, err := getSecret(ctx, endpoint.Spec.ClientTLS.RootCA.SecretKeyRef.Name, namespace, *endpoint.Spec.ClientTLS.RootCA.SecretKeyRef, kubeClient)
		if err != nil {
			return err
		}

		endpoint.Spec.ClientTLS.RootCA.Value = &v
	}

	return nil
}

// GetHTTPEndpoint returns a specified http endpoint object.
func (a *apiServer) GetHTTPEndpoint(ctx context.Context, in *operatorv1pb.GetResiliencyRequest) (*operatorv1pb.GetHTTPEndpointResponse, error) {
	if _, err := authz.Request(ctx, in.GetNamespace()); err != nil {
		return nil, err
	}

	key := types.NamespacedName{Namespace: in.GetNamespace(), Name: in.GetName()}
	var endpointConfig httpendpointsapi.HTTPEndpoint
	if err := a.Client.Get(ctx, key, &endpointConfig); err != nil {
		return nil, fmt.Errorf("error getting http endpoint: %w", err)
	}
	b, err := json.Marshal(&endpointConfig)
	if err != nil {
		return nil, fmt.Errorf("error marshalling http endpoint: %w", err)
	}
	return &operatorv1pb.GetHTTPEndpointResponse{
		HttpEndpoint: b,
	}, nil
}

// ListHTTPEndpoints gets the list of applied http endpoints.
func (a *apiServer) ListHTTPEndpoints(ctx context.Context, in *operatorv1pb.ListHTTPEndpointsRequest) (*operatorv1pb.ListHTTPEndpointsResponse, error) {
	if _, err := authz.Request(ctx, in.GetNamespace()); err != nil {
		return nil, err
	}

	resp := &operatorv1pb.ListHTTPEndpointsResponse{
		HttpEndpoints: [][]byte{},
	}

	var endpoints httpendpointsapi.HTTPEndpointList
	if err := a.Client.List(ctx, &endpoints, &client.ListOptions{
		Namespace: in.GetNamespace(),
	}); err != nil {
		return nil, fmt.Errorf("error listing http endpoints: %w", err)
	}

	for i, item := range endpoints.Items {
		e := endpoints.Items[i]
		err := processHTTPEndpointSecrets(ctx, &e, item.Namespace, a.Client)
		if err != nil {
			log.Warnf("error processing secrets for http endpoint '%s/%s': %s", item.Namespace, item.Name, err)
			return &operatorv1pb.ListHTTPEndpointsResponse{}, err
		}

		b, err := json.Marshal(e)
		if err != nil {
			log.Warnf("Error unmarshalling http endpoints: %s", err)
			continue
		}
		resp.HttpEndpoints = append(resp.GetHttpEndpoints(), b)
	}

	return resp, nil
}

// HTTPEndpointUpdate handles HTTP endpoint update streaming for a connected client.
// Each client connection gets its own client loop that watches the informer
// and sends updates over the gRPC stream.
func (a *apiServer) HTTPEndpointUpdate(in *operatorv1pb.HTTPEndpointUpdateRequest, srv operatorv1pb.Operator_HTTPEndpointUpdateServer) error { //nolint:nosnakecase
	if a.closed.Load() {
		return errors.New("server is closed")
	}

	log.Info("sidecar connected for http endpoint updates")

	ctx := srv.Context()

	// Verify authorization via informer's WatchUpdates, which checks SPIFFE ID
	ch, cancel, err := a.endpointInformer.WatchUpdates(ctx, in.GetNamespace())
	if err != nil {
		return err
	}

	stream, err := sender.New(srv)
	if err != nil {
		return err
	}

	// Create a client for this connection
	client := loopsclient.New(loopsclient.Options[httpendpointsapi.HTTPEndpoint]{
		EventCh:        ch,
		CancelWatch:    cancel,
		Stream:         stream,
		Namespace:      in.GetNamespace(),
		KubeClient:     a.Client,
		ProcessSecrets: processHTTPEndpointSecrets,
	})
	defer client.CacheLoop()

	// Run the client - this will block until context is done or event channel closes
	if err := client.Run(ctx); err != nil {
		log.Warnf("http endpoint client loop ended with error: %s", err)
	}

	return nil
}
