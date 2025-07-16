/*
Copyright 2025 The Dapr Authors
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

package fake

import (
	"context"

	"google.golang.org/protobuf/types/known/anypb"

	"github.com/dapr/dapr/pkg/apphealth"
	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
)

type Fake struct {
	fnGetAppConfig func(ctx context.Context, appID string) (*config.ApplicationConfig, error)
	fnInvokeMethod func(ctx context.Context, req *invokev1.InvokeMethodRequest, appID string) (*invokev1.InvokeMethodResponse, error)
	fnHealthProbe  func(ctx context.Context) (*apphealth.Status, error)
	fnSetAppHealth func(ah *apphealth.AppHealth)
	fnTriggerJob   func(ctx context.Context, name string, data *anypb.Any) (*invokev1.InvokeMethodResponse, error)
}

func New() *Fake {
	return &Fake{
		fnGetAppConfig: func(ctx context.Context, appID string) (*config.ApplicationConfig, error) {
			return &config.ApplicationConfig{}, nil
		},
		fnInvokeMethod: func(ctx context.Context, req *invokev1.InvokeMethodRequest, appID string) (*invokev1.InvokeMethodResponse, error) {
			return &invokev1.InvokeMethodResponse{}, nil
		},
		fnHealthProbe: func(ctx context.Context) (*apphealth.Status, error) {
			return &apphealth.Status{}, nil
		},
		fnSetAppHealth: func(ah *apphealth.AppHealth) {},
		fnTriggerJob: func(ctx context.Context, name string, data *anypb.Any) (*invokev1.InvokeMethodResponse, error) {
			return &invokev1.InvokeMethodResponse{}, nil
		},
	}
}

func (f *Fake) WithGetAppConfig(fn func(context.Context, string) (*config.ApplicationConfig, error)) *Fake {
	f.fnGetAppConfig = fn
	return f
}

func (f *Fake) WithInvokeMethod(fn func(context.Context, *invokev1.InvokeMethodRequest, string) (*invokev1.InvokeMethodResponse, error)) *Fake {
	f.fnInvokeMethod = fn
	return f
}

func (f *Fake) WithHealthProbe(fn func(context.Context) (*apphealth.Status, error)) *Fake {
	f.fnHealthProbe = fn
	return f
}

func (f *Fake) WithSetAppHealth(fn func(*apphealth.AppHealth)) *Fake {
	f.fnSetAppHealth = fn
	return f
}

func (f *Fake) WithTriggerJob(fn func(context.Context, string, *anypb.Any) (*invokev1.InvokeMethodResponse, error)) *Fake {
	f.fnTriggerJob = fn
	return f
}

func (f *Fake) GetAppConfig(ctx context.Context, appID string) (*config.ApplicationConfig, error) {
	return f.fnGetAppConfig(ctx, appID)
}

func (f *Fake) InvokeMethod(ctx context.Context, req *invokev1.InvokeMethodRequest, appID string) (*invokev1.InvokeMethodResponse, error) {
	return f.fnInvokeMethod(ctx, req, appID)
}

func (f *Fake) HealthProbe(ctx context.Context) (*apphealth.Status, error) {
	return f.fnHealthProbe(ctx)
}

func (f *Fake) SetAppHealth(ah *apphealth.AppHealth) {
	f.fnSetAppHealth(ah)
}

func (f *Fake) TriggerJob(ctx context.Context, name string, data *anypb.Any) (*invokev1.InvokeMethodResponse, error) {
	return f.fnTriggerJob(ctx, name, data)
}
