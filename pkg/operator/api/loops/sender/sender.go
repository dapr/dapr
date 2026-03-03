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

package sender

import (
	"errors"

	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

type Interface interface {
	Send([]byte, operatorv1pb.ResourceEventType) error
}

func New(stream any) (Interface, error) {
	switch s := stream.(type) {
	case operatorv1pb.Operator_ComponentUpdateServer:
		return &component{
			stream: s,
		}, nil

	case operatorv1pb.Operator_SubscriptionUpdateServer:
		return &subscription{
			stream: s,
		}, nil

	case operatorv1pb.Operator_HTTPEndpointUpdateServer:
		return &httpendpoint{
			stream: s,
		}, nil

	case operatorv1pb.Operator_ConfigurationUpdateServer:
		return &configuration{
			stream: s,
		}, nil

	case operatorv1pb.Operator_ResiliencyUpdateServer:
		return &resiliency{
			stream: s,
		}, nil

	default:
		return nil, errors.New("unsupported stream type")
	}
}

type component struct {
	stream operatorv1pb.Operator_ComponentUpdateServer
}

func (c *component) Send(data []byte, eventType operatorv1pb.ResourceEventType) error {
	return c.stream.Send(&operatorv1pb.ComponentUpdateEvent{
		Component: data,
		Type:      eventType,
	})
}

type subscription struct {
	stream operatorv1pb.Operator_SubscriptionUpdateServer
}

func (s *subscription) Send(data []byte, eventType operatorv1pb.ResourceEventType) error {
	return s.stream.Send(&operatorv1pb.SubscriptionUpdateEvent{
		Subscription: data,
		Type:         eventType,
	})
}

type httpendpoint struct {
	stream operatorv1pb.Operator_HTTPEndpointUpdateServer
}

func (h *httpendpoint) Send(data []byte, eventType operatorv1pb.ResourceEventType) error {
	return h.stream.Send(&operatorv1pb.HTTPEndpointUpdateEvent{
		HttpEndpoints: data,
		Type:          eventType,
	})
}

type configuration struct {
	stream operatorv1pb.Operator_ConfigurationUpdateServer
}

func (c *configuration) Send(data []byte, eventType operatorv1pb.ResourceEventType) error {
	return c.stream.Send(&operatorv1pb.ConfigurationUpdateEvent{
		Configuration: data,
		Type:          eventType,
	})
}

type resiliency struct {
	stream operatorv1pb.Operator_ResiliencyUpdateServer
}

func (r *resiliency) Send(data []byte, eventType operatorv1pb.ResourceEventType) error {
	return r.stream.Send(&operatorv1pb.ResiliencyUpdateEvent{
		Resiliency: data,
		Type:       eventType,
	})
}
