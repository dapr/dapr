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

package acl

import (
	"testing"

	fuzz "github.com/AdaLogics/go-fuzz-headers"

	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/config"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	"github.com/dapr/dapr/pkg/security/spiffe"
)

func init() {
	log.SetOutputLevel(logger.FatalLevel)
}

// Tests ParseAccessControlSpec with random values
func FuzzParseAccessControlSpec(f *testing.F) {
	f.Fuzz(func(t *testing.T, specData []byte) {
		ff := fuzz.NewConsumer(specData)
		s := &config.AccessControlSpec{}
		ff.GenerateStruct(s)
		b, err := ff.GetBool()
		if err != nil {
			return
		}
		_, _ = ParseAccessControlSpec(s, b)
	})
}

// Tests normalizeOperation with a random string.
// normalizeOperation has complex parsing routines
// which this test focuses on.
func FuzzPurellTest(f *testing.F) {
	f.Fuzz(func(t *testing.T, operation string) {
		normalizeOperation(operation)
	})
}

// Tests isOperationAllowedByAccessControlPolicy with random values
func FuzzIsOperationAllowedByAccessControlPolicy(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte, inputOperation string, httpVerbInd int32) {
		ff := fuzz.NewConsumer(data)
		spiffeID := &spiffe.Parsed{}
		ff.GenerateStruct(spiffeID)
		srcAppID, err := ff.GetString()
		if err != nil {
			return
		}
		if srcAppID == "" {
			return
		}
		httpVerbInt32 := httpVerbInd % 10
		httpVerb := commonv1pb.HTTPExtension_Verb(httpVerbInt32)
		isHTTP, err := ff.GetBool()
		if err != nil {
			return
		}
		accessControlList := &config.AccessControlList{}
		ff.GenerateStruct(accessControlList)
		accessControlList.DefaultAction = config.DenyAccess
		_, _ = isOperationAllowedByAccessControlPolicy(spiffeID, inputOperation, httpVerb, isHTTP, accessControlList)
	})
}
