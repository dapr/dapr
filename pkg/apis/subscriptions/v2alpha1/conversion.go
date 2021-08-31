package v2alpha1

import (
	"errors"

	"sigs.k8s.io/controller-runtime/pkg/conversion"

	"github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
)

// +kubebuilder:docs-gen:collapse=Imports

/*
Our "spoke" versions need to implement the
[`Convertible`](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/conversion?tab=doc#Convertible)
interface.  Namely, they'll need `ConvertTo` and `ConvertFrom` methods to convert to/from
the hub version.
*/

/*
ConvertTo is expected to modify its argument to contain the converted object.
Most of the conversion is straightforward copying, except for converting our changed field.
*/
// ConvertTo converts this Subscription to the Hub version (v1).
func (s *Subscription) ConvertTo(dstRaw conversion.Hub) error {
	dst, ok := dstRaw.(*v1alpha1.Subscription)
	if !ok {
		return errors.New("expected to to convert to *v1alpha1.Subscription")
	}

	// ObjectMeta
	dst.ObjectMeta = s.ObjectMeta

	// Spec
	dst.Spec.Pubsubname = s.Spec.Pubsubname
	dst.Spec.Topic = s.Spec.Topic
	dst.Spec.Metadata = s.Spec.Metadata
	dst.Spec.Route = s.Spec.Routes.Default

	// +kubebuilder:docs-gen:collapse=rote conversion
	return nil
}

/*
ConvertFrom is expected to modify its receiver to contain the converted object.
Most of the conversion is straightforward copying, except for converting our changed field.
*/
// ConvertFrom converts from the Hub version (v1) to this version.
func (s *Subscription) ConvertFrom(srcRaw conversion.Hub) error {
	src, ok := srcRaw.(*v1alpha1.Subscription)
	if !ok {
		return errors.New("expected to to convert from *v1alpha1.Subscription")
	}

	// ObjectMeta
	s.ObjectMeta = src.ObjectMeta

	// Spec
	s.Spec.Pubsubname = src.Spec.Pubsubname
	s.Spec.Topic = src.Spec.Topic
	s.Spec.Metadata = src.Spec.Metadata
	s.Spec.Routes.Default = src.Spec.Route

	// +kubebuilder:docs-gen:collapse=rote conversion
	return nil
}
