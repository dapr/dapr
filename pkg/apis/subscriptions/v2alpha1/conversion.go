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
		return errors.New("expected to convert to *v1alpha1.Subscription")
	}

	// Copy scopes
	dst.Scopes = s.Scopes

	// ObjectMeta
	dst.ObjectMeta = s.ObjectMeta

	// Spec
	dst.Spec.Pubsubname = s.Spec.Pubsubname
	dst.Spec.Topic = s.Spec.Topic
	dst.Spec.Metadata = s.Spec.Metadata
	dst.Spec.Route = s.Spec.Routes.Default
	dst.Spec.DeadLetterTopic = s.Spec.DeadLetterTopic
	dst.Spec.BulkSubscribe = *convertBulkSubscriptionV2alpha1ToV1alpha1(&s.Spec.BulkSubscribe)

	// +kubebuilder:docs-gen:collapse=rote conversion
	return nil
}

func convertBulkSubscriptionV2alpha1ToV1alpha1(in *BulkSubscribe) *v1alpha1.BulkSubscribe {
	out := v1alpha1.BulkSubscribe{
		Enabled:            in.Enabled,
		MaxMessagesCount:   in.MaxMessagesCount,
		MaxAwaitDurationMs: in.MaxAwaitDurationMs,
	}
	return &out
}

/*
ConvertFrom is expected to modify its receiver to contain the converted object.
Most of the conversion is straightforward copying, except for converting our changed field.
*/
// ConvertFrom converts from the Hub version (v1) to this version.
func (s *Subscription) ConvertFrom(srcRaw conversion.Hub) error {
	src, ok := srcRaw.(*v1alpha1.Subscription)
	if !ok {
		return errors.New("expected to convert from *v1alpha1.Subscription")
	}

	// Copy scopes
	s.Scopes = src.Scopes

	// ObjectMeta
	s.ObjectMeta = src.ObjectMeta

	// Spec
	s.Spec.Pubsubname = src.Spec.Pubsubname
	s.Spec.Topic = src.Spec.Topic
	s.Spec.Metadata = src.Spec.Metadata
	s.Spec.Routes.Default = src.Spec.Route
	s.Spec.DeadLetterTopic = src.Spec.DeadLetterTopic
	s.Spec.BulkSubscribe = *convertBulkSubscriptionV1alpha1ToV2alpha1(&src.Spec.BulkSubscribe)

	// +kubebuilder:docs-gen:collapse=rote conversion
	return nil
}

func convertBulkSubscriptionV1alpha1ToV2alpha1(in *v1alpha1.BulkSubscribe) *BulkSubscribe {
	out := BulkSubscribe{
		Enabled:            in.Enabled,
		MaxMessagesCount:   in.MaxMessagesCount,
		MaxAwaitDurationMs: in.MaxAwaitDurationMs,
	}
	return &out
}
