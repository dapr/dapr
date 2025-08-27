//go:build allcomponents

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

package components

import (
	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/bindings/alicloud/dingtalk/webhook"
	bindingsLoader "github.com/dapr/dapr/pkg/components/bindings"
	"github.com/dapr/kit/logger"
)

func init() {
	bindingsLoader.DefaultRegistry.RegisterInputBinding(func(l logger.Logger) bindings.InputBinding {
		return webhook.NewDingTalkWebhook(l)
	}, "dingtalk.webhook", "alicloud.dingtalk.webhook")
	bindingsLoader.DefaultRegistry.RegisterOutputBinding(func(l logger.Logger) bindings.OutputBinding {
		return webhook.NewDingTalkWebhook(l)
	}, "dingtalk.webhook", "alicloud.dingtalk.webhook")
}
