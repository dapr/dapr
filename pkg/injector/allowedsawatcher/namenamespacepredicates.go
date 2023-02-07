package allowedsawatcher

import (
	"github.com/dapr/dapr/utils"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"strings"
)

func getNameNamespacePredicates(name string) predicate.Predicate {
	wild, exact, err := getNamespaceNames(name)
	if err != nil {
		log.Fatalf("problems getting namespace predicate setup, err: %s", err)
	}
	var preds []predicate.Predicate

	for nsPrefix, values := range wild {
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
		})
	}
	for ns, values := range exact {
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
		})
	}

	return predicate.Or(preds...)
}
