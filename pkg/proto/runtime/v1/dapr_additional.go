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

package runtime

// This file contains additional, hand-written methods added to the generated objects.

// SubtleCryptoRequests is an interface for all Subtle*Request structs.
type SubtleCryptoRequests interface {
	// SetComponentName sets the value of the ComponentName property.
	SetComponentName(name string)
}

func (x *SubtleGetKeyAlpha1Request) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

func (x *SubtleEncryptAlpha1Request) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

func (x *SubtleDecryptAlpha1Request) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

func (x *SubtleWrapKeyAlpha1Request) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

func (x *SubtleUnwrapKeyAlpha1Request) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

func (x *SubtleSignAlpha1Request) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}

func (x *SubtleVerifyAlpha1Request) SetComponentName(name string) {
	if x != nil {
		x.ComponentName = name
	}
}
