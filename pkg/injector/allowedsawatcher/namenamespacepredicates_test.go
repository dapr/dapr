package allowedsawatcher

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func Test_getNameNamespacePredicates(t *testing.T) {
	tests := []struct {
		name           string
		namespaceNames string
		objectMeta     metav1.ObjectMeta
		wantCreate     bool
		wantDelete     bool
		wantUpdate     bool
		wantError      bool
	}{
		{
			name:           "equalPredicate",
			namespaceNames: "ns:sa",
			objectMeta:     metav1.ObjectMeta{Name: "sa", Namespace: "ns"},
			wantCreate:     true,
			wantDelete:     true,
			wantUpdate:     false,
			wantError:      false,
		},
		{
			name:           "equalPredicateNoMatch",
			namespaceNames: "ns:sa,ns:sb,ns:sc",
			objectMeta:     metav1.ObjectMeta{Name: "sd", Namespace: "ns"},
			wantCreate:     false,
			wantDelete:     false,
			wantUpdate:     false,
			wantError:      false,
		},
		{
			name:           "equalPredicateNoMatchWrongNS",
			namespaceNames: "ns:sa,ns:sb,ns:sc",
			objectMeta:     metav1.ObjectMeta{Name: "sd", Namespace: "ns2"},
			wantCreate:     false,
			wantDelete:     false,
			wantUpdate:     false,
			wantError:      false,
		},
		{
			name:           "equalNamespacePrefixSA",
			namespaceNames: "ns:vc-sa*",
			objectMeta:     metav1.ObjectMeta{Name: "vc-sa-1234", Namespace: "ns"},
			wantCreate:     true,
			wantDelete:     true,
			wantUpdate:     false,
			wantError:      false,
		},
		{
			name:           "equalNamespacePrefixSABadPrefix",
			namespaceNames: "ns:vc-sa*sa",
			objectMeta:     metav1.ObjectMeta{Name: "vc-sa-1234", Namespace: "ns"},
			wantCreate:     true,
			wantDelete:     true,
			wantUpdate:     false,
			wantError:      true,
		},
		{
			name:           "equalNamespacePrefixSANoMatch",
			namespaceNames: "ns:vc-sa*",
			objectMeta:     metav1.ObjectMeta{Name: "vc-sb-1234", Namespace: "ns"},
			wantCreate:     false,
			wantDelete:     false,
			wantUpdate:     false,
			wantError:      false,
		},
		{
			name:           "equalNamespaceMultiplePrefixSA",
			namespaceNames: "ns:vc-sa*,ns:vc-sb*",
			objectMeta:     metav1.ObjectMeta{Name: "vc-sb-1234", Namespace: "ns"},
			wantCreate:     true,
			wantDelete:     true,
			wantUpdate:     false,
			wantError:      false,
		},
		{
			name:           "prefixNamespaceMultiplePrefixSA",
			namespaceNames: "name*:vc-sa*,name*:vc-sb*",
			objectMeta:     metav1.ObjectMeta{Name: "vc-sb-1234", Namespace: "namespace"},
			wantCreate:     true,
			wantDelete:     true,
			wantUpdate:     false,
			wantError:      false,
		},
		{
			name:           "prefixNamespaceMultiplePrefixSANoMatch",
			namespaceNames: "name*:vc-sa*,name*:vc-sb*",
			objectMeta:     metav1.ObjectMeta{Name: "vc-sb-1234", Namespace: "namspace"},
			wantCreate:     false,
			wantDelete:     false,
			wantUpdate:     false,
			wantError:      false,
		},
	}
	for _, tc := range tests {
		pred, err := getNameNamespacePredicates(tc.namespaceNames)
		if tc.wantError {
			assert.Error(t, err, "expecting error")
			continue
		}
		sa := &corev1.ServiceAccount{
			ObjectMeta: tc.objectMeta,
		}
		assert.Equal(t, tc.wantCreate, pred.Create(event.CreateEvent{Object: sa}))
		assert.Equal(t, tc.wantDelete, pred.Delete(event.DeleteEvent{Object: sa}))
		assert.Equal(t, tc.wantUpdate, pred.Update(event.UpdateEvent{ObjectOld: sa, ObjectNew: sa}))
		assert.Equal(t, tc.wantUpdate, pred.Generic(event.GenericEvent{Object: sa}))
	}
}
