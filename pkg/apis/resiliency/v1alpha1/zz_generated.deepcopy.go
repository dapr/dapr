//go:build !ignore_autogenerated
// +build !ignore_autogenerated

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
	"k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ActorPolicyNames) DeepCopyInto(out *ActorPolicyNames) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ActorPolicyNames.
func (in *ActorPolicyNames) DeepCopy() *ActorPolicyNames {
	if in == nil {
		return nil
	}
	out := new(ActorPolicyNames)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *CircuitBreaker) DeepCopyInto(out *CircuitBreaker) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new CircuitBreaker.
func (in *CircuitBreaker) DeepCopy() *CircuitBreaker {
	if in == nil {
		return nil
	}
	out := new(CircuitBreaker)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ComponentPolicyNames) DeepCopyInto(out *ComponentPolicyNames) {
	*out = *in
	out.Inbound = in.Inbound
	out.Outbound = in.Outbound
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ComponentPolicyNames.
func (in *ComponentPolicyNames) DeepCopy() *ComponentPolicyNames {
	if in == nil {
		return nil
	}
	out := new(ComponentPolicyNames)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *EndpointPolicyNames) DeepCopyInto(out *EndpointPolicyNames) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new EndpointPolicyNames.
func (in *EndpointPolicyNames) DeepCopy() *EndpointPolicyNames {
	if in == nil {
		return nil
	}
	out := new(EndpointPolicyNames)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Policies) DeepCopyInto(out *Policies) {
	*out = *in
	if in.Timeouts != nil {
		in, out := &in.Timeouts, &out.Timeouts
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.Retries != nil {
		in, out := &in.Retries, &out.Retries
		*out = make(map[string]Retry, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.CircuitBreakers != nil {
		in, out := &in.CircuitBreakers, &out.CircuitBreakers
		*out = make(map[string]CircuitBreaker, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Policies.
func (in *Policies) DeepCopy() *Policies {
	if in == nil {
		return nil
	}
	out := new(Policies)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PolicyNames) DeepCopyInto(out *PolicyNames) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PolicyNames.
func (in *PolicyNames) DeepCopy() *PolicyNames {
	if in == nil {
		return nil
	}
	out := new(PolicyNames)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Resiliency) DeepCopyInto(out *Resiliency) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	if in.Scopes != nil {
		in, out := &in.Scopes, &out.Scopes
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Resiliency.
func (in *Resiliency) DeepCopy() *Resiliency {
	if in == nil {
		return nil
	}
	out := new(Resiliency)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *Resiliency) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResiliencyList) DeepCopyInto(out *ResiliencyList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]Resiliency, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResiliencyList.
func (in *ResiliencyList) DeepCopy() *ResiliencyList {
	if in == nil {
		return nil
	}
	out := new(ResiliencyList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ResiliencyList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResiliencySpec) DeepCopyInto(out *ResiliencySpec) {
	*out = *in
	in.Policies.DeepCopyInto(&out.Policies)
	in.Targets.DeepCopyInto(&out.Targets)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResiliencySpec.
func (in *ResiliencySpec) DeepCopy() *ResiliencySpec {
	if in == nil {
		return nil
	}
	out := new(ResiliencySpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Retry) DeepCopyInto(out *Retry) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Retry.
func (in *Retry) DeepCopy() *Retry {
	if in == nil {
		return nil
	}
	out := new(Retry)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Targets) DeepCopyInto(out *Targets) {
	*out = *in
	if in.Apps != nil {
		in, out := &in.Apps, &out.Apps
		*out = make(map[string]EndpointPolicyNames, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.Actors != nil {
		in, out := &in.Actors, &out.Actors
		*out = make(map[string]ActorPolicyNames, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.Components != nil {
		in, out := &in.Components, &out.Components
		*out = make(map[string]ComponentPolicyNames, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Targets.
func (in *Targets) DeepCopy() *Targets {
	if in == nil {
		return nil
	}
	out := new(Targets)
	in.DeepCopyInto(out)
	return out
}
