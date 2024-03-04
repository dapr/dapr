//go:build !ignore_autogenerated

/*
Copyright The Dapr Authors.

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

// Code generated by controller-gen. DO NOT EDIT.

package v1alpha1

import (
	"github.com/dapr/dapr/pkg/apis/common"
	"k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Auth) DeepCopyInto(out *Auth) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Auth.
func (in *Auth) DeepCopy() *Auth {
	if in == nil {
		return nil
	}
	out := new(Auth)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *HTTPEndpoint) DeepCopyInto(out *HTTPEndpoint) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	out.Auth = in.Auth
	in.Scoped.DeepCopyInto(&out.Scoped)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new HTTPEndpoint.
func (in *HTTPEndpoint) DeepCopy() *HTTPEndpoint {
	if in == nil {
		return nil
	}
	out := new(HTTPEndpoint)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *HTTPEndpoint) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *HTTPEndpointList) DeepCopyInto(out *HTTPEndpointList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]HTTPEndpoint, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new HTTPEndpointList.
func (in *HTTPEndpointList) DeepCopy() *HTTPEndpointList {
	if in == nil {
		return nil
	}
	out := new(HTTPEndpointList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *HTTPEndpointList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *HTTPEndpointSpec) DeepCopyInto(out *HTTPEndpointSpec) {
	*out = *in
	if in.Headers != nil {
		in, out := &in.Headers, &out.Headers
		*out = make([]common.NameValuePair, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.ClientTLS != nil {
		in, out := &in.ClientTLS, &out.ClientTLS
		*out = new(common.TLS)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new HTTPEndpointSpec.
func (in *HTTPEndpointSpec) DeepCopy() *HTTPEndpointSpec {
	if in == nil {
		return nil
	}
	out := new(HTTPEndpointSpec)
	in.DeepCopyInto(out)
	return out
}
