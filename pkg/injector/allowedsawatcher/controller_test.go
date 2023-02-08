package allowedsawatcher

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes/scheme"

	"k8s.io/apimachinery/pkg/types"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ctlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockInjector struct {
	lastAdd    []string
	lastDelete []string
}

func (m *mockInjector) Run(ctx context.Context, f func()) {}
func (m *mockInjector) UpdateAllowedAuthUIDs(addAuthUIDs []string, deleteAuthUIDs []string) {
	m.lastAdd = addAuthUIDs
	m.lastDelete = deleteAuthUIDs
}

func Test_allowedSAWatcher_Reconcile(t *testing.T) {
	firstSA := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "sa", Namespace: "ns", UID: "first"},
	}
	fakeClient := ctlfake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(firstSA).
		Build()

	mockInj := &mockInjector{}
	r := newAllowedSAWatcher(fakeClient, mockInj)
	ctx := context.Background()
	t.Run("createServiceAccount", func(t *testing.T) {
		reqNamespacedName := types.NamespacedName{Namespace: "ns", Name: "sa"}
		resp, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: reqNamespacedName,
		})
		assert.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, resp)
		assert.Equal(t, "first", r.namespaceNameToUIDs[types.NamespacedName{Namespace: "ns", Name: "sa"}])
		assert.Equal(t, []string{"first"}, mockInj.lastAdd)
		assert.Nil(t, mockInj.lastDelete)
	})

	t.Run("deleteServiceAccount", func(t *testing.T) {
		reqNamespacedName := types.NamespacedName{Namespace: "ns", Name: "sa"}
		assert.NoError(t, fakeClient.Delete(ctx, firstSA))
		resp, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: reqNamespacedName,
		})
		assert.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, resp)
		assert.Equal(t, "", r.namespaceNameToUIDs[types.NamespacedName{Namespace: "ns", Name: "sa"}])
		assert.Nil(t, mockInj.lastAdd)
		assert.Equal(t, []string{"first"}, mockInj.lastDelete)
	})
}
