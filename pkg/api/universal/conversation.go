/*
Copyright 2022 The Dapr Authors
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

package universal

import (
	"context"

	"github.com/dapr/components-contrib/conversation"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
)

func (a *Universal) ConverseAlpha1(ctx context.Context, req *runtimev1pb.ConversationAlpha1Request) (*runtimev1pb.ConversationAlpha1Response, error) {
	// valid component
	if a.compStore.ConversationsLen() == 0 {
		err := messages.ErrConversationNotFound
		a.logger.Debug(err)
		return nil, err
	}

	component, ok := a.compStore.GetConversation(req.Name)
	if !ok {
		err := messages.ErrConversationNotFound.WithFormat(req.Name)
		a.logger.Debug(err)
		return nil, err
	}

	// prepare request
	request := conversation.ConversationRequest{
		Inputs: req.GetInputs(),
		// Parameters: req.GetParameters(),
		Endpoints: req.GetEndpoints(),
		Model:     req.GetModel(),
		Key:       req.GetKey(),
		Policy:    conversation.LoadBalancingPolicy(req.GetPolicy()),
	}

	// do call
	policyRunner := resiliency.NewRunner[*conversation.ConversationResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(req.Name, resiliency.Conversation),
	)

	resp, err := policyRunner(func(ctx context.Context) (*conversation.ConversationResponse, error) {
		rResp, rErr := component.Converse(ctx, &request)
		return rResp, rErr
	})

	if err != nil {
		err = messages.ErrConversationInvoke.WithFormat(req.Name, err.Error())
		a.logger.Debug(err)
		return nil, err
	}

	// handle response
	var response *runtimev1pb.ConversationAlpha1Response
	a.logger.Debug(response)
	if resp != nil {
		// TODO: support multiple results
		result := resp.Outputs[0]
		response = &runtimev1pb.ConversationAlpha1Response{
			Outputs: []*runtimev1pb.ConversationAlpha1Result{
				&runtimev1pb.ConversationAlpha1Result{
					Result: result.Result,
					// Parameters: result.Parameters,
				},
			},
		}
	}
	return response, nil
}
