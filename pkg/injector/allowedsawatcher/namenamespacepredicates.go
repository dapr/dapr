package allowedsawatcher

import (
	"strings"

	"github.com/dapr/dapr/utils"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

func getNameNamespacePredicates(name string) (predicate.Predicate, error) {
	prefixed, equal, err := getNamespaceNames(name)
	if err != nil {
		return nil, err
	}
	var preds []predicate.Predicate

	for nsPrefix, values := range prefixed {
		preds = append(preds, predicate.Funcs{
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
		})
	}
	for ns, values := range equal {
		preds = append(preds, predicate.Funcs{
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
		})
	}

	return predicate.Or(preds...), nil
}
