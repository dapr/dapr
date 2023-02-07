package allowedsawatcher

import (
	"fmt"
	"github.com/dapr/dapr/utils"
	"strings"
)

type equalPrefixLists struct {
	equal  []string
	prefix []string
}

const minPrefixLength = 5 // try to avoid "kube-"

var forbiddenPrefixes = []string{
	"kube-",
	"dapr-",
}
var forbiddenNS = []string{
	"kube-system",
	"dapr-system",
}

// getNamespaceNames from the csv provided by the user of sa:ns values, we create two maps
// one with namespace prefixes and one with namespace exact values
// inside each map we can have exact name or prefixed names
// note there might be prefixes that cover other prefixes but we are not filtering it for now
func getNamespaceNames(s string) (prefixed, equal map[string]*equalPrefixLists, err error) {
	for _, nameNamespace := range strings.Split(s, ",") {
		saNs := strings.Split(nameNamespace, ":")
		if len(saNs) != 2 {
			return nil, nil, fmt.Errorf("service account namespace pair not following expected format 'serviceaccountname:namespace'")
		}
		sa, ns := saNs[0], saNs[1]
		sa = strings.TrimSpace(sa)
		ns = strings.TrimSpace(ns)
		if len(ns) == 0 || len(sa) == 0 {
			return nil, nil, fmt.Errorf("service account name and namespace cannot be empty (even for default namespace)")
		}
		if nsPrefix, ok := getPrefix(ns); ok {
			if len(nsPrefix) < minPrefixLength {
				return nil, nil, fmt.Errorf("prefixes for namespace should have a minimum length of %d chars and provided one %s has only %d chars", minPrefixLength, nsPrefix, len(nsPrefix))
			}
			if utils.Contains(forbiddenPrefixes, nsPrefix) {
				return nil, nil, fmt.Errorf("prefixes for namespace cannot start with %s and provided one was %s", strings.Join(forbiddenPrefixes, ", "), nsPrefix)
			}
			if prefixed == nil {
				prefixed = make(map[string]*equalPrefixLists)
			}
			if _, ok := prefixed[nsPrefix]; !ok {
				prefixed[nsPrefix] = &equalPrefixLists{}
			}
			if err = getSaExactPrefix(sa, prefixed[nsPrefix]); err != nil {
				return nil, nil, err
			}
		} else {
			if utils.Contains(forbiddenNS, ns) {
				return nil, nil, fmt.Errorf("namespace cannot be one of the following system namespaces %s and provided one was %s", strings.Join(forbiddenNS, ", "), ns)
			}
			if equal == nil {
				equal = make(map[string]*equalPrefixLists)
			}
			if _, ok := equal[ns]; !ok {
				equal[ns] = &equalPrefixLists{}
			}
			if err = getSaExactPrefix(sa, equal[ns]); err != nil {
				return nil, nil, err
			}
		}
	}
	return prefixed, equal, nil
}

func getSaExactPrefix(sa string, namespaceNames *equalPrefixLists) error {
	if saPrefix, ok := getPrefix(sa); ok {
		if len(saPrefix) < minPrefixLength {
			return fmt.Errorf("prefixes for namespace and name should have a minimum length of %d chars and provided one %s has only %d chars", minPrefixLength, saPrefix, len(saPrefix))
		}
		if !utils.Contains(namespaceNames.prefix, saPrefix) {
			namespaceNames.prefix = append(namespaceNames.prefix, saPrefix)
		}
	} else {
		if !utils.Contains(namespaceNames.equal, sa) {
			namespaceNames.equal = append(namespaceNames.equal, sa)
		}
	}
	return nil
}

func getPrefix(s string) (string, bool) {
	wildcardIndex := strings.Index(s, "*")
	if wildcardIndex == -1 {
		return "", false
	}
	if wildcardIndex != (len(s) - 1) {
		log.Fatalf("we only allow a single wildcard to indicate prefix matching for allowed servicename or namespace, and we were provided with %s", s)
	}
	return s[:len(s)-1], true
}
