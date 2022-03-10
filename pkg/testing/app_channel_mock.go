/*
Copyright 2021 The Dapr Authors
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

package testing

import (
	"context"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
)

type FailingAppChannel struct {
	Failure Failure
	KeyFunc func(req *invokev1.InvokeMethodRequest) string
}

func (f *FailingAppChannel) GetBaseAddress() string {
	return ""
}

func (f *FailingAppChannel) GetAppConfig() (*config.ApplicationConfig, error) {
	return nil, nil
}

func (f *FailingAppChannel) InvokeMethod(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	err := f.Failure.PerformFailure(f.KeyFunc(req))
	if err != nil {
		return invokev1.NewInvokeMethodResponse(500, "Failure!", []*anypb.Any{}), err
	}

	return invokev1.NewInvokeMethodResponse(200, "Success", []*anypb.Any{}), nil
}
