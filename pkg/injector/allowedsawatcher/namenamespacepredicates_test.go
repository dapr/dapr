package allowedsawatcher

import (
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"testing"
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
			namespaceNames: "sa:ns",
			objectMeta:     metav1.ObjectMeta{Name: "sa", Namespace: "ns"},
			wantCreate:     true,
			wantDelete:     true,
			wantUpdate:     false,
			wantError:      false,
		},
		{
			name:           "equalPredicateNoMatch",
			namespaceNames: "sa:ns,sb:ns,sc:ns",
			objectMeta:     metav1.ObjectMeta{Name: "sd", Namespace: "ns"},
			wantCreate:     false,
			wantDelete:     false,
			wantUpdate:     false,
			wantError:      false,
		},
		{
			name:           "equalPredicateNoMatchWrongNS",
			namespaceNames: "sa:ns,sb:ns,sc:ns",
			objectMeta:     metav1.ObjectMeta{Name: "sd", Namespace: "ns2"},
			wantCreate:     false,
			wantDelete:     false,
			wantUpdate:     false,
			wantError:      false,
		},
		{
			name:           "equalNamespacePrefixSA",
			namespaceNames: "vc-sa*:ns",
			objectMeta:     metav1.ObjectMeta{Name: "vc-sa-1234", Namespace: "ns"},
			wantCreate:     true,
			wantDelete:     true,
			wantUpdate:     false,
			wantError:      false,
		},
		{
			name:           "equalNamespacePrefixSABadPrefix",
			namespaceNames: "vc-sa*sa:ns",
			objectMeta:     metav1.ObjectMeta{Name: "vc-sa-1234", Namespace: "ns"},
			wantCreate:     true,
			wantDelete:     true,
			wantUpdate:     false,
			wantError:      true,
		},
		{
			name:           "equalNamespacePrefixSANoMatch",
			namespaceNames: "vc-sa*:ns",
			objectMeta:     metav1.ObjectMeta{Name: "vc-sb-1234", Namespace: "ns"},
			wantCreate:     false,
			wantDelete:     false,
			wantUpdate:     false,
			wantError:      false,
		},
		{
			name:           "equalNamespaceMultiplePrefixSA",
			namespaceNames: "vc-sa*:ns,vc-sb*:ns",
			objectMeta:     metav1.ObjectMeta{Name: "vc-sb-1234", Namespace: "ns"},
			wantCreate:     true,
			wantDelete:     true,
			wantUpdate:     false,
			wantError:      false,
		},
		{
			name:           "prefixNamespaceMultiplePrefixSA",
			namespaceNames: "vc-sa*:name*,vc-sb*:name*",
			objectMeta:     metav1.ObjectMeta{Name: "vc-sb-1234", Namespace: "namespace"},
			wantCreate:     true,
			wantDelete:     true,
			wantUpdate:     false,
			wantError:      false,
		},
		{
			name:           "prefixNamespaceMultiplePrefixSANoMatch",
			namespaceNames: "vc-sa*:name*,vc-sb*:name*",
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
