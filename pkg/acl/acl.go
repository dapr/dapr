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
	"encoding/asn1"
	"errors"
	"fmt"
	"strings"

	"github.com/PuerkitoBio/purell"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"

	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
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
				operationName = strings.ToLower(operationName)
			}

			operationActions := config.AccessControlListOperationAction{
				OperationName: operationName,
				VerbAction:    make(map[string]string),
			}

			// Iterate over all the http verbs and create a map and set the action for fast lookup
			for _, verb := range appPolicy.HTTPVerb {
				operationActions.VerbAction[verb] = appPolicy.Action
			}

			// Store the operation action for grpc invocations where no http verb is specified
			operationActions.OperationAction = appPolicy.Action

			operationPolicy.PutOperationAction(operationName, &operationActions)
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

// GetAndParseSpiffeID retrieves the SPIFFE Id from the cert and parses it.
func GetAndParseSpiffeID(ctx context.Context) (*config.SpiffeID, error) {
	spiffeID, err := getSpiffeID(ctx)
	if err != nil {
		return nil, err
	}

	id, err := parseSpiffeID(spiffeID)
	return id, err
}

func parseSpiffeID(spiffeID string) (*config.SpiffeID, error) {
	if spiffeID == "" {
		return nil, errors.New("input spiffe id string is empty")
	}

	if !strings.HasPrefix(spiffeID, config.SpiffeIDPrefix) {
		return nil, fmt.Errorf("input spiffe id: %s is invalid", spiffeID)
	}

	// The SPIFFE Id will be of the format: spiffe://<trust-domain/ns/<namespace>/<app-id>
	parts := strings.Split(spiffeID, "/")
	if len(parts) < 6 {
		return nil, fmt.Errorf("input spiffe id: %s is invalid", spiffeID)
	}

	var id config.SpiffeID
	id.TrustDomain = parts[2]
	id.Namespace = parts[4]
	id.AppID = parts[5]

	return &id, nil
}

func getSpiffeID(ctx context.Context) (string, error) {
	var spiffeID string
	peer, ok := peer.FromContext(ctx)
	if ok {
		if peer == nil || peer.AuthInfo == nil {
			return "", errors.New("unable to retrieve peer auth info")
		}

		tlsInfo := peer.AuthInfo.(credentials.TLSInfo)

		// https://www.ietf.org/rfc/rfc3280.txt
		oid := asn1.ObjectIdentifier{2, 5, 29, 17}

		for _, crt := range tlsInfo.State.PeerCertificates {
			for _, ext := range crt.Extensions {
				if ext.Id.Equal(oid) {
					var sequence asn1.RawValue
					if rest, err := asn1.Unmarshal(ext.Value, &sequence); err != nil {
						log.Debug(err)
						continue
					} else if len(rest) != 0 {
						log.Debug("the SAN extension is incorrectly encoded")
						continue
					}

					if !sequence.IsCompound || sequence.Tag != asn1.TagSequence || sequence.Class != asn1.ClassUniversal {
						log.Debug("the SAN extension is incorrectly encoded")
						continue
					}

					for bytes := sequence.Bytes; len(bytes) > 0; {
						var rawValue asn1.RawValue
						var err error

						bytes, err = asn1.Unmarshal(bytes, &rawValue)
						if err != nil {
							return "", err
						}

						spiffeID = string(rawValue.Bytes)
						if strings.HasPrefix(spiffeID, config.SpiffeIDPrefix) {
							return spiffeID, nil
						}
					}
				}
			}
		}
	}

	return "", nil
}

func normalizeOperation(operation string) (string, error) {
	s, err := purell.NormalizeURLString(operation, purell.FlagsUsuallySafeGreedy|purell.FlagRemoveDuplicateSlashes)
	if err != nil {
		return "", err
	}
	return s, nil
}

func ApplyAccessControlPolicies(ctx context.Context, operation string, httpVerb commonv1pb.HTTPExtension_Verb, isHTTP bool, acl *config.AccessControlList) (bool, string) {
	// Apply access control list filter
	spiffeID, err := GetAndParseSpiffeID(ctx)
	if err != nil {
		// Apply the default action
		log.Debugf("error while reading spiffe id from client cert: %v. applying default global policy action", err.Error())
	}
	var appID, trustDomain, namespace string
	if spiffeID != nil {
		appID = spiffeID.AppID
		namespace = spiffeID.Namespace
		trustDomain = spiffeID.TrustDomain
	}

	operation, err = normalizeOperation(operation)
	var errMessage string

	if err != nil {
		errMessage = fmt.Sprintf("error in method normalization: %s", err)
		log.Debugf(errMessage)
		return false, errMessage
	}

	action, actionPolicy := IsOperationAllowedByAccessControlPolicy(spiffeID, appID, operation, httpVerb, isHTTP, acl)
	emitACLMetrics(actionPolicy, appID, trustDomain, namespace, operation, httpVerb.String(), action)

	if !action {
		errMessage = fmt.Sprintf("access control policy has denied access to appid: %s operation: %s verb: %s", appID, operation, httpVerb)
		log.Debugf(errMessage)
	}

	return action, errMessage
}

func emitACLMetrics(actionPolicy, appID, trustDomain, namespace, operation, verb string, action bool) {
	if action {
		switch actionPolicy {
		case config.ActionPolicyApp:
			diag.DefaultMonitoring.RequestAllowedByAppAction(appID, trustDomain, namespace, operation, verb, action)
		case config.ActionPolicyGlobal:
			diag.DefaultMonitoring.RequestAllowedByGlobalAction(appID, trustDomain, namespace, operation, verb, action)
		}
	} else {
		switch actionPolicy {
		case config.ActionPolicyApp:
			diag.DefaultMonitoring.RequestBlockedByAppAction(appID, trustDomain, namespace, operation, verb, action)
		case config.ActionPolicyGlobal:
			diag.DefaultMonitoring.RequestBlockedByGlobalAction(appID, trustDomain, namespace, operation, verb, action)
		}
	}
}

// IsOperationAllowedByAccessControlPolicy determines if access control policies allow the operation on the target app.
func IsOperationAllowedByAccessControlPolicy(spiffeID *config.SpiffeID, srcAppID string, inputOperation string, httpVerb commonv1pb.HTTPExtension_Verb, isHTTP bool, accessControlList *config.AccessControlList) (bool, string) {
	if accessControlList == nil {
		// No access control list is provided. Do nothing
		return isActionAllowed(config.AllowAccess), ""
	}

	action := accessControlList.DefaultAction
	actionPolicy := config.ActionPolicyGlobal

	if srcAppID == "" {
		// Did not receive the src app id correctly
		return isActionAllowed(action), actionPolicy
	}

	if spiffeID == nil {
		// Could not retrieve spiffe id or it is invalid. Apply global default action
		return isActionAllowed(action), actionPolicy
	}

	// Look up the src app id in the in-memory table. The key is appID||namespace
	key := getKeyForAppID(srcAppID, spiffeID.Namespace)
	appPolicy, found := accessControlList.PolicySpec[key]

	if !found {
		// no policies found for this src app id. Apply global default action
		return isActionAllowed(action), actionPolicy
	}

	// Match trust domain
	if appPolicy.TrustDomain != spiffeID.TrustDomain {
		return isActionAllowed(action), actionPolicy
	}

	// Match namespace
	if appPolicy.Namespace != spiffeID.Namespace {
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

	// If HTTP, make case-insensitive
	if isHTTP {
		inputOperation = strings.ToLower(inputOperation)
	}

	operationPolicy := appPolicy.AppOperationActions.Search(inputOperation)

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

	return isActionAllowed(action), actionPolicy
}

func isActionAllowed(action string) bool {
	return strings.EqualFold(action, config.AllowAccess)
}

func getKeyForAppID(appID, namespace string) string {
	key := appID + "||" + namespace
	return key
}
