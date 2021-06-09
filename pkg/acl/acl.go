// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

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
func ParseAccessControlSpec(accessControlSpec config.AccessControlSpec, protocol string) (*config.AccessControlList, error) {
	if accessControlSpec.TrustDomain == "" &&
		accessControlSpec.DefaultAction == "" &&
		(accessControlSpec.AppPolicies == nil || len(accessControlSpec.AppPolicies) == 0) {
		// No ACL has been specified
		log.Debugf("No Access control policy specified")
		return nil, nil
	}

	var accessControlList config.AccessControlList
	accessControlList.PolicySpec = make(map[string]config.AccessControlListPolicySpec)
	accessControlList.DefaultAction = strings.ToLower(accessControlSpec.DefaultAction)

	accessControlList.TrustDomain = accessControlSpec.TrustDomain
	if accessControlSpec.TrustDomain == "" {
		accessControlList.TrustDomain = config.DefaultTrustDomain
	}

	accessControlList.DefaultAction = accessControlSpec.DefaultAction
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

		operationPolicy := make(map[string]config.AccessControlListOperationAction)

		// Iterate over all the operations and create a map for fast lookup
		for _, appPolicy := range appPolicySpec.AppOperationActions {
			// The operation name might be specified as /invoke/*
			// Store the prefix as the key and use the remainder as post fix for faster lookups
			// Also, prepend "/" in case it is missing in the operation name
			operation := appPolicy.Operation
			if !strings.HasPrefix(operation, "/") {
				operation = "/" + operation
			}
			operationPrefix, operationPostfix := getOperationPrefixAndPostfix(operation)

			if protocol == config.HTTPProtocol {
				operationPrefix = strings.ToLower(operationPrefix)
				operationPostfix = strings.ToLower(operationPostfix)
			}

			operationActions := config.AccessControlListOperationAction{
				OperationPostFix: operationPostfix,
				VerbAction:       make(map[string]string),
			}

			// Iterate over all the http verbs and create a map and set the action for fast lookup
			for _, verb := range appPolicy.HTTPVerb {
				operationActions.VerbAction[verb] = appPolicy.Action
			}

			// Store the operation action for grpc invocations where no http verb is specified
			operationActions.OperationAction = appPolicy.Action

			operationPolicy[operationPrefix] = operationActions
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

func ApplyAccessControlPolicies(ctx context.Context, operation string, httpVerb commonv1pb.HTTPExtension_Verb, appProtocol string, acl *config.AccessControlList) (bool, string) {
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

	action, actionPolicy := IsOperationAllowedByAccessControlPolicy(spiffeID, appID, operation, httpVerb, appProtocol, acl)
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
func IsOperationAllowedByAccessControlPolicy(spiffeID *config.SpiffeID, srcAppID string, inputOperation string, httpVerb commonv1pb.HTTPExtension_Verb, appProtocol string, accessControlList *config.AccessControlList) (bool, string) {
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

	inputOperationPrefix, inputOperationPostfix := getOperationPrefixAndPostfix(inputOperation)

	// If HTTP, make case-insensitive
	if appProtocol == config.HTTPProtocol {
		inputOperationPrefix = strings.ToLower(inputOperationPrefix)
		inputOperationPostfix = strings.ToLower(inputOperationPostfix)
	}

	// The acl may specify the operation in a format /invoke/*, get and match only the prefix first
	operationPolicy, found := appPolicy.AppOperationActions[inputOperationPrefix]
	if found {
		// The ACL might have the operation specified as /invoke/*. Here "/*" is stored as the postfix.
		// Match postfix

		if strings.Contains(operationPolicy.OperationPostFix, "/*") {
			if !strings.HasPrefix(inputOperationPostfix, strings.ReplaceAll(operationPolicy.OperationPostFix, "/*", "")) {
				return isActionAllowed(action), actionPolicy
			}
		} else {
			if operationPolicy.OperationPostFix != inputOperationPostfix {
				return isActionAllowed(action), actionPolicy
			}
		}

		// Operation prefix and postfix match. Now check the operation specific policy
		if appProtocol == config.HTTPProtocol {
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
		} else if appProtocol == config.GRPCProtocol {
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

// getOperationPrefixAndPostfix returns an app operation prefix and postfix.
// The prefix can be stored in the in-memory ACL for fast lookup.
// e.g.: /invoke/*, prefix = /invoke, postfix = /*.
func getOperationPrefixAndPostfix(operation string) (string, string) {
	operationParts := strings.Split(operation, "/")
	operationPrefix := "/" + operationParts[1]
	operationPostfix := "/" + strings.Join(operationParts[2:], "/")

	return operationPrefix, operationPostfix
}
