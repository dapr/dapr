/*
Copyright 2021 The Dapr Authors
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

//nolint:nosnakecase
package acl

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	"github.com/dapr/dapr/pkg/security/spiffe"
)

var log = logger.NewLogger("dapr.acl")

// ParseAccessControlSpec creates an in-memory copy of the Access Control Spec for fast lookup.
func ParseAccessControlSpec(accessControlSpec *config.AccessControlSpec, isHTTP bool) (*config.AccessControlList, error) {
	if accessControlSpec == nil ||
		(accessControlSpec.TrustDomain == "" &&
			accessControlSpec.DefaultAction == "" &&
			len(accessControlSpec.AppPolicies) == 0) {
		// No ACL has been specified
		log.Debugf("No Access control policy specified")
		return nil, nil
	}

	accessControlList := config.AccessControlList{
		PolicySpec:    make(map[string]config.AccessControlListPolicySpec),
		DefaultAction: strings.ToLower(accessControlSpec.DefaultAction),
		TrustDomain:   accessControlSpec.TrustDomain,
	}

	if accessControlSpec.TrustDomain == "" {
		accessControlList.TrustDomain = config.DefaultTrustDomain
	}

	if accessControlSpec.DefaultAction == "" {
		if accessControlSpec.AppPolicies == nil || len(accessControlSpec.AppPolicies) > 0 {
			// Some app level policies have been specified but not default global action is set. Default to more secure option - Deny
			log.Warnf("No global default action has been specified. Setting default global action as Deny")
			accessControlList.DefaultAction = config.DenyAccess
		} else {
			// An empty ACL has been specified. Set default global action to Allow
			accessControlList.DefaultAction = config.AllowAccess
		}
	}

	var invalidTrustDomain []string
	var invalidNamespace []string
	var invalidAppName bool
	accessControlList.PolicySpec = make(map[string]config.AccessControlListPolicySpec)
	for _, appPolicySpec := range accessControlSpec.AppPolicies {
		invalid := false
		if appPolicySpec.AppName == "" {
			invalidAppName = true
		}
		if appPolicySpec.TrustDomain == "" {
			invalidTrustDomain = append(invalidTrustDomain, appPolicySpec.AppName)
			invalid = true
		}
		if appPolicySpec.Namespace == "" {
			invalidNamespace = append(invalidNamespace, appPolicySpec.AppName)
			invalid = true
		}

		if invalid || invalidAppName {
			// An invalid config was found for this app. No need to continue parsing the spec for this app
			continue
		}

		operationPolicy := config.NewTrie()
		operations := make(map[string]*config.AccessControlListOperationAction)

		// Iterate over all the operations and create a map for fast lookup
		for _, appPolicy := range appPolicySpec.AppOperationActions {
			// The operation name might be specified as /invoke/*
			// Store the prefix as the key and use the remainder as post fix for faster lookups
			// Also, prepend "/" in case it is missing in the operation name
			operationName := appPolicy.Operation
			if !strings.HasPrefix(operationName, "/") {
				operationName = "/" + operationName
			}

			if isHTTP {
				_, operationName, _ = canonicalHTTPOperation(operationName)
			}

			// Operations that resolve to the same name share one entry.
			operationActions, ok := operations[operationName]
			if !ok {
				operationActions = &config.AccessControlListOperationAction{
					OperationName: operationName,
					VerbAction:    make(map[string]string),
				}
				operations[operationName] = operationActions
				operationPolicy.PutOperationAction(operationName, operationActions)
			}

			// Iterate over all the http verbs and create a map and set the action for fast lookup
			for _, verb := range appPolicy.HTTPVerb {
				operationActions.VerbAction[verb] = mergeAction(operationActions.VerbAction[verb], appPolicy.Action)
			}

			// Store the operation action for grpc invocations where no http verb is specified
			operationActions.OperationAction = mergeAction(operationActions.OperationAction, appPolicy.Action)
		}
		aclPolicySpec := config.AccessControlListPolicySpec{
			AppName:             appPolicySpec.AppName,
			DefaultAction:       appPolicySpec.DefaultAction,
			TrustDomain:         appPolicySpec.TrustDomain,
			Namespace:           appPolicySpec.Namespace,
			AppOperationActions: operationPolicy,
		}

		// The policy spec can have the same appID which belongs to different namespaces
		key := getKeyForAppID(aclPolicySpec.AppName, aclPolicySpec.Namespace)
		accessControlList.PolicySpec[key] = aclPolicySpec
	}

	if len(invalidTrustDomain) > 0 || len(invalidNamespace) > 0 || invalidAppName {
		return nil, fmt.Errorf(
			"invalid access control spec. missing trustdomain for apps: %v, missing namespace for apps: %v, missing app name on at least one of the app policies: %v",
			invalidTrustDomain,
			invalidNamespace,
			invalidAppName)
	}

	return &accessControlList, nil
}

func ApplyAccessControlPolicies(ctx context.Context, operation string, httpVerb commonv1pb.HTTPExtension_Verb, isHTTP bool, acl *config.AccessControlList) (bool, string) {
	// Apply access control list filter
	spiffeID, ok, err := spiffe.FromGRPCContext(ctx)
	if err != nil {
		log.Debugf("failed to get SPIFFE ID from gRPC connection context: %v", err)
		return false, err.Error()
	}

	if !ok {
		// Apply the default action
		log.Debugf("Error while reading spiffe id from client cert. applying default global policy action")
	}

	// The operation is expected to be already normalized by the caller
	// (NormalizeMethod at the service invocation entry point). For HTTP apps the
	// ACL matches on canonicalHTTPOperation(operation), the path the app routes
	// on, but the operation sent to the app is not modified.
	action, actionPolicy := isOperationAllowedByAccessControlPolicy(spiffeID, operation, httpVerb, isHTTP, acl)
	emitACLMetrics(spiffeID, actionPolicy, action)

	var errMessage string
	if !action {
		errMessage = fmt.Sprintf("access control policy has denied access to id: %s operation: %s verb: %s", spiffeID.URL(), operation, httpVerb)
		log.Debug(errMessage)
	}

	return action, errMessage
}

func emitACLMetrics(spiffeID *spiffe.Parsed, actionPolicy string, action bool) {
	if action {
		switch actionPolicy {
		case config.ActionPolicyApp:
			diag.DefaultMonitoring.RequestAllowedByAppAction(spiffeID)
		case config.ActionPolicyGlobal:
			diag.DefaultMonitoring.RequestAllowedByGlobalAction(spiffeID)
		}
	} else {
		switch actionPolicy {
		case config.ActionPolicyApp:
			diag.DefaultMonitoring.RequestBlockedByAppAction(spiffeID)
		case config.ActionPolicyGlobal:
			diag.DefaultMonitoring.RequestBlockedByGlobalAction(spiffeID)
		}
	}
}

// isOperationAllowedByAccessControlPolicy determines if access control policies allow the operation on the target app.
func isOperationAllowedByAccessControlPolicy(spiffeID *spiffe.Parsed, inputOperation string, httpVerb commonv1pb.HTTPExtension_Verb, isHTTP bool, accessControlList *config.AccessControlList) (bool, string) {
	if accessControlList == nil {
		// No access control list is provided. Do nothing
		return isActionAllowed(config.AllowAccess), ""
	}

	action := accessControlList.DefaultAction
	actionPolicy := config.ActionPolicyGlobal

	if spiffeID == nil {
		// Could not retrieve spiffe id or it is invalid. Apply global default action
		return isActionAllowed(action), actionPolicy
	}

	// Look up the src app id in the in-memory table. The key is appID||namespace
	key := getKeyForAppID(spiffeID.AppID(), spiffeID.Namespace())
	appPolicy, found := accessControlList.PolicySpec[key]

	if !found {
		// no policies found for this src app id. Apply global default action
		return isActionAllowed(action), actionPolicy
	}

	// Match trust domain
	if appPolicy.TrustDomain != spiffeID.TrustDomain().String() {
		return isActionAllowed(action), actionPolicy
	}

	// Match namespace
	if appPolicy.Namespace != spiffeID.Namespace() {
		return isActionAllowed(action), actionPolicy
	}

	if appPolicy.DefaultAction != "" {
		// Since the app has specified a default action, this point onwards,
		// default action is the default action specified in the spec for the app
		action = appPolicy.DefaultAction
		actionPolicy = config.ActionPolicyApp
	}

	// the in-memory table has operations stored in the format "/operation name".
	// Prepend a "/" in case missing so that the match works
	if !strings.HasPrefix(inputOperation, "/") {
		inputOperation = "/" + inputOperation
	}

	operations := []string{inputOperation}
	if isHTTP {
		decoded, stripped, ok := canonicalHTTPOperation(inputOperation)
		if !ok {
			return false, actionPolicy
		}
		// An app may or may not drop ";" path parameters before routing, so
		// the operation must be allowed both without and with them.
		operations = []string{stripped, decoded}
	}

	for _, operation := range operations {
		if !isActionAllowed(operationAction(appPolicy, operation, httpVerb, isHTTP, action)) {
			return false, actionPolicy
		}
	}

	return true, actionPolicy
}

// operationAction returns the action of the rule in appPolicy that matches
// operation, or action if no rule matches.
func operationAction(appPolicy config.AccessControlListPolicySpec, operation string, httpVerb commonv1pb.HTTPExtension_Verb, isHTTP bool, action string) string {
	operationPolicy := appPolicy.AppOperationActions.Search(operation)

	if operationPolicy != nil {
		// Operation prefix and postfix match. Now check the operation specific policy
		if isHTTP {
			if httpVerb != commonv1pb.HTTPExtension_NONE {
				verbAction, found := operationPolicy.VerbAction[httpVerb.String()]
				if found {
					// An action for a specific verb is matched
					action = verbAction
				} else {
					verbAction, found = operationPolicy.VerbAction["*"]
					if found {
						// The verb matched the wildcard "*"
						action = verbAction
					}
				}
			} else {
				// No matching verb found in the operation specific policies.
				action = appPolicy.DefaultAction
			}
		} else {
			// No http verb match is needed.
			action = operationPolicy.OperationAction
		}
	}

	return action
}

// mergeAction returns the action for two rules on the same operation and
// verb: deny wins.
func mergeAction(current, next string) string {
	if current != "" && !isActionAllowed(current) {
		return current
	}
	return next
}

// canonicalHTTPOperation returns an HTTP operation as an app may route it,
// lowercased: decoded has percent-escapes decoded once, and stripped is
// decoded with ";" path parameters dropped from each segment. An escaped "*"
// stays escaped, so it cannot become a wildcard. ok is false, and op is not
// decoded, if op has an invalid escape or an escaped "/", or if dropping
// parameters leaves an empty, "." or ".." segment. The app would not route
// such an operation as checked, so requests with it are denied.
func canonicalHTTPOperation(op string) (decoded, stripped string, ok bool) {
	parts := strings.Split(strings.ReplaceAll(op, "%2A", "%2a"), "%2a")
	ok = true
	for i, part := range parts {
		var err error
		if parts[i], err = url.PathUnescape(part); err != nil {
			ok = false
		}
	}
	decoded = strings.Join(parts, "%2a")
	if !ok || strings.Count(decoded, "/") != strings.Count(op, "/") {
		decoded, ok = op, false
	}

	segs := strings.Split(decoded, "/")
	for i, seg := range segs {
		var cut bool
		segs[i], _, cut = strings.Cut(seg, ";")
		if segs[i] == "." || segs[i] == ".." || (cut && segs[i] == "") {
			ok = false
		}
	}
	return strings.ToLower(decoded), strings.ToLower(strings.Join(segs, "/")), ok
}

func isActionAllowed(action string) bool {
	return strings.EqualFold(action, config.AllowAccess)
}

func getKeyForAppID(appID, namespace string) string {
	return appID + "||" + namespace
}
