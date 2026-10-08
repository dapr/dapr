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

package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

func TestStripUntrustedCallerIdentity(t *testing.T) {
	md := internalv1pb.MetadataToInternalMetadata(map[string][]string{
		invokev1.CallerIDHeader:        {"admin"},
		invokev1.CallerNamespaceHeader: {"kube-system"},
		"Dapr-Caller-App-Id":           {"admin"},
		"Dapr-Caller-Namespace":        {"kube-system"},
		"DAPR-CALLER-APP-ID":           {"admin"},
		invokev1.CalleeIDHeader:        {"callee"},
		"other":                        {"kept"},
	})
	StripUntrustedCallerIdentity(md)
	assert.Equal(t, internalv1pb.MetadataToInternalMetadata(map[string][]string{
		invokev1.CalleeIDHeader: {"callee"},
		"other":                 {"kept"},
	}), md)

	StripUntrustedCallerIdentity(nil)
}
