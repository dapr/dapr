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

package testing

import (
	"fmt"

	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	"github.com/dapr/dapr/pkg/scopes"
)

const (
	TestRuntimeConfigID = "consumer0"
)

func GetFakeProperties() map[string]string {
	return map[string]string{
		"host":                    "localhost",
		"password":                "fakePassword",
		"consumerID":              TestRuntimeConfigID,
		scopes.SubscriptionScopes: fmt.Sprintf("%s=topic0,topic1,topic10,topic11", TestRuntimeConfigID),
		scopes.PublishingScopes:   fmt.Sprintf("%s=topic0,topic1,topic10,topic11", TestRuntimeConfigID),
		scopes.ProtectedTopics:    "topic10,topic11,topic12",
	}
}

func GetFakeMetadataItems() []commonapi.NameValuePair {
	return []commonapi.NameValuePair{
		{
			Name: "host",
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("localhost"),
				},
			},
		},
		{
			Name: "password",
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("fakePassword"),
				},
			},
		},
		{
			Name: "consumerID",
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte(TestRuntimeConfigID),
				},
			},
		},
		{
			Name: scopes.SubscriptionScopes,
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte(fmt.Sprintf("%s=topic0,topic1,topic10,topic11", TestRuntimeConfigID)),
				},
			},
		},
		{
			Name: scopes.PublishingScopes,
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte(fmt.Sprintf("%s=topic0,topic1,topic10,topic11", TestRuntimeConfigID)),
				},
			},
		},
		{
			Name: scopes.ProtectedTopics,
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("topic10,topic11,topic12"),
				},
			},
		},
	}
}
