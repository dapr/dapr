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

package acl

import (
	"testing"

	fuzz "github.com/AdamKorcz/go-fuzz-headers-1"

	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/config"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
)

func init() {
	log.SetOutputLevel(logger.FatalLevel)
}

func FuzzParseAccessControlSpec(f *testing.F) {
	f.Fuzz(func(t *testing.T, specData []byte) {
		ff := fuzz.NewConsumer(specData)
		s := &config.AccessControlSpec{}
		ff.GenerateStruct(s)
		b, err := ff.GetBool()
		if err != nil {
			return
		}
		_, _ = ParseAccessControlSpec(*s, b)
	})
}

func FuzzIsOperationAllowedByAccessControlPolicy(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		ff := fuzz.NewConsumer(data)
		spiffeID := &config.SpiffeID{}
		ff.GenerateStruct(spiffeID)
		srcAppID, err := ff.GetString()
		if err != nil {
			return
		}
		if srcAppID == "" {
			return
		}
		inputOperation, err := ff.GetString()
		if err != nil {
			return
		}
		httpVerbInd, err := ff.GetInt()
		if err != nil {
			return
		}
		httpVerbInt32 := int32(httpVerbInd % 10)
		httpVerb := commonv1pb.HTTPExtension_Verb(httpVerbInt32)
		isHTTP, err := ff.GetBool()
		if err != nil {
			return
		}
		accessControlList := &config.AccessControlList{}
		ff.GenerateStruct(accessControlList)
		accessControlList.DefaultAction = config.DenyAccess
		_, _ = IsOperationAllowedByAccessControlPolicy(spiffeID, srcAppID, inputOperation, httpVerb, isHTTP, accessControlList)
	})
}
