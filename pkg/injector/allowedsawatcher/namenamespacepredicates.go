package allowedsawatcher

import (
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/dapr/dapr/utils"
)

func getNameNamespacePredicates(name string) (predicate.Predicate, error) {
	prefixed, equal, err := getNamespaceNames(name)
	if err != nil {
		return nil, err
	}
	preds := make([]predicate.Predicate, len(prefixed)+len(equal))
	var i int

	for nsPrefix, values := range prefixed {
		preds[i] = predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				if strings.HasPrefix(e.Object.GetNamespace(), nsPrefix) {
					return utils.ContainsPrefixed(values.prefix, e.Object.GetName()) || utils.Contains(values.equal, e.Object.GetName())
				}
				return false
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				if strings.HasPrefix(e.Object.GetNamespace(), nsPrefix) {
					return utils.ContainsPrefixed(values.prefix, e.Object.GetName()) || utils.Contains(values.equal, e.Object.GetName())
				}
				return false
			},
			UpdateFunc:  func(updateEvent event.UpdateEvent) bool { return false },
			GenericFunc: func(event.GenericEvent) bool { return false },
		}
		i++
	}
	for ns, values := range equal {
		preds[i] = predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				if ns == e.Object.GetNamespace() {
					return utils.ContainsPrefixed(values.prefix, e.Object.GetName()) || utils.Contains(values.equal, e.Object.GetName())
				}
				return false
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				if ns == e.Object.GetNamespace() {
					return utils.ContainsPrefixed(values.prefix, e.Object.GetName()) || utils.Contains(values.equal, e.Object.GetName())
				}
				return false
			},
			UpdateFunc:  func(updateEvent event.UpdateEvent) bool { return false },
			GenericFunc: func(event.GenericEvent) bool { return false },
		}
		i++
	}

	return predicate.Or(preds...), nil
}
