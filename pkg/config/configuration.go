// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package config

import (
	"context"
	"encoding/asn1"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"time"

	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"github.com/pkg/errors"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	yaml "gopkg.in/yaml.v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/dapr/dapr/pkg/proto/common/v1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.configuration")

const (
	operatorCallTimeout = time.Second * 5
	operatorMaxRetries  = 100
	AllowAccess         = "allow"
	DenyAccess          = "deny"
	DefaultTrustDomain  = "public"
	DefaultNamespace    = "default"
	ActionPolicyApp     = "app"
	ActionPolicyGlobal  = "global"
	SpiffeIDPrefix      = "spiffe://"
	HTTPProtocol        = "http"
	GRPCProtocol        = "grpc"
)

// Configuration is an internal (and duplicate) representation of Dapr's Configuration CRD.
type Configuration struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`
	// See https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#metadata
	metav1.ObjectMeta `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	// See https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#spec-and-status
	Spec ConfigurationSpec `json:"spec" yaml:"spec"`
}

// AccessControlList is an in-memory access control list config for fast lookup
type AccessControlList struct {
	DefaultAction string
	TrustDomain   string
	PolicySpec    map[string]AccessControlListPolicySpec
}

// AccessControlListPolicySpec is an in-memory access control list config per app for fast lookup
type AccessControlListPolicySpec struct {
	AppName             string
	DefaultAction       string
	TrustDomain         string
	Namespace           string
	AppOperationActions map[string]AccessControlListOperationAction
}

// AccessControlListOperationAction is an in-memory access control list config per operation for fast lookup
type AccessControlListOperationAction struct {
	VerbAction       map[string]string
	OperationPostFix string
	OperationAction  string
}

type ConfigurationSpec struct {
	HTTPPipelineSpec   PipelineSpec       `json:"httpPipeline,omitempty" yaml:"httpPipeline,omitempty"`
	TracingSpec        TracingSpec        `json:"tracing,omitempty" yaml:"tracing,omitempty"`
	MTLSSpec           MTLSSpec           `json:"mtls,omitempty"`
	MetricSpec         MetricSpec         `json:"metric,omitempty" yaml:"metric,omitempty"`
	Secrets            SecretsSpec        `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	AccessControlSpec  AccessControlSpec  `json:"accessControl,omitempty" yaml:"accessControl,omitempty"`
	NameResolutionSpec NameResolutionSpec `json:"nameResolution,omitempty" yaml:"nameResolution,omitempty"`
}

type SecretsSpec struct {
	Scopes []SecretsScope `json:"scopes"`
}

// SecretsScope defines the scope for secrets
type SecretsScope struct {
	DefaultAccess  string   `json:"defaultAccess,omitempty" yaml:"defaultAccess,omitempty"`
	StoreName      string   `json:"storeName" yaml:"storeName"`
	AllowedSecrets []string `json:"allowedSecrets,omitempty" yaml:"allowedSecrets,omitempty"`
	DeniedSecrets  []string `json:"deniedSecrets,omitempty" yaml:"deniedSecrets,omitempty"`
}

type PipelineSpec struct {
	Handlers []HandlerSpec `json:"handlers" yaml:"handlers"`
}

type HandlerSpec struct {
	Name         string       `json:"name" yaml:"name"`
	Type         string       `json:"type" yaml:"type"`
	Version      string       `json:"version" yaml:"version"`
	SelectorSpec SelectorSpec `json:"selector,omitempty" yaml:"selector,omitempty"`
}

type SelectorSpec struct {
	Fields []SelectorField `json:"fields" yaml:"fields"`
}

type SelectorField struct {
	Field string `json:"field" yaml:"field"`
	Value string `json:"value" yaml:"value"`
}

type TracingSpec struct {
	SamplingRate string     `json:"samplingRate" yaml:"samplingRate"`
	Stdout       bool       `json:"stdout" yaml:"stdout"`
	Zipkin       ZipkinSpec `json:"zipkin" yaml:"zipkin"`
}

// ZipkinSpec defines Zipkin trace configurations
type ZipkinSpec struct {
	EndpointAddress string `json:"endpointAddress" yaml:"endpointAddress"`
}

// MetricSpec configuration for metrics
type MetricSpec struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

// AppPolicySpec defines the policy data structure for each app
type AppPolicySpec struct {
	AppName             string         `json:"appId" yaml:"appId"`
	DefaultAction       string         `json:"defaultAction" yaml:"defaultAction"`
	TrustDomain         string         `json:"trustDomain" yaml:"trustDomain"`
	Namespace           string         `json:"namespace" yaml:"namespace"`
	AppOperationActions []AppOperation `json:"operations" yaml:"operations"`
}

// AppOperation defines the data structure for each app operation
type AppOperation struct {
	Operation string   `json:"name" yaml:"name"`
	HTTPVerb  []string `json:"httpVerb" yaml:"httpVerb"`
	Action    string   `json:"action" yaml:"action"`
}

// AccessControlSpec is the spec object in ConfigurationSpec
type AccessControlSpec struct {
	DefaultAction string          `json:"defaultAction" yaml:"defaultAction"`
	TrustDomain   string          `json:"trustDomain" yaml:"trustDomain"`
	AppPolicies   []AppPolicySpec `json:"policies" yaml:"policies"`
}

type NameResolutionSpec struct {
	Component     string      `json:"component" yaml:"component"`
	Version       string      `json:"version" yaml:"version"`
	Configuration interface{} `json:"configuration" yaml:"configuration"`
}

type MTLSSpec struct {
	Enabled          bool   `json:"enabled"`
	WorkloadCertTTL  string `json:"workloadCertTTL"`
	AllowedClockSkew string `json:"allowedClockSkew"`
}

// SpiffeID represents the separated fields in a spiffe id
type SpiffeID struct {
	TrustDomain string
	Namespace   string
	AppID       string
}

// LoadDefaultConfiguration returns the default config
func LoadDefaultConfiguration() *Configuration {
	return &Configuration{
		Spec: ConfigurationSpec{
			TracingSpec: TracingSpec{
				SamplingRate: "",
			},
			MetricSpec: MetricSpec{
				Enabled: true,
			},
			AccessControlSpec: AccessControlSpec{
				DefaultAction: AllowAccess,
				TrustDomain:   "public",
			},
		},
	}
}

// LoadStandaloneConfiguration gets the path to a config file and loads it into a configuration
func LoadStandaloneConfiguration(config string) (*Configuration, string, error) {
	_, err := os.Stat(config)
	if err != nil {
		return nil, "", err
	}

	b, err := ioutil.ReadFile(config)
	if err != nil {
		return nil, "", err
	}

	// Parse environment variables from yaml
	b = []byte(os.ExpandEnv(string(b)))

	conf := LoadDefaultConfiguration()
	err = yaml.Unmarshal(b, conf)
	if err != nil {
		return nil, string(b), err
	}
	err = sortAndValidateSecretsConfiguration(conf)
	if err != nil {
		return nil, string(b), err
	}

	return conf, string(b), nil
}

// LoadKubernetesConfiguration gets configuration from the Kubernetes operator with a given name
func LoadKubernetesConfiguration(config, namespace string, operatorClient operatorv1pb.OperatorClient) (*Configuration, error) {
	resp, err := operatorClient.GetConfiguration(context.Background(), &operatorv1pb.GetConfigurationRequest{
		Name:      config,
		Namespace: namespace,
	}, grpc_retry.WithMax(operatorMaxRetries), grpc_retry.WithPerRetryTimeout(operatorCallTimeout))
	if err != nil {
		return nil, err
	}
	if resp.GetConfiguration() == nil {
		return nil, errors.Errorf("configuration %s not found", config)
	}
	conf := LoadDefaultConfiguration()
	err = json.Unmarshal(resp.GetConfiguration(), conf)
	if err != nil {
		return nil, err
	}

	err = sortAndValidateSecretsConfiguration(conf)
	if err != nil {
		return nil, err
	}

	return conf, nil
}

// Validate the secrets configuration and sort the allow and deny lists if present.
func sortAndValidateSecretsConfiguration(conf *Configuration) error {
	scopes := conf.Spec.Secrets.Scopes
	set := sets.NewString()
	for _, scope := range scopes {
		// validate scope
		if set.Has(scope.StoreName) {
			return errors.Errorf("%q storeName is repeated in secrets configuration", scope.StoreName)
		}
		if scope.DefaultAccess != "" &&
			!strings.EqualFold(scope.DefaultAccess, AllowAccess) &&
			!strings.EqualFold(scope.DefaultAccess, DenyAccess) {
			return errors.Errorf("defaultAccess %q can be either allow or deny", scope.DefaultAccess)
		}
		set.Insert(scope.StoreName)

		// modify scope
		sort.Strings(scope.AllowedSecrets)
		sort.Strings(scope.DeniedSecrets)
	}

	return nil
}

// Check if the secret is allowed to be accessed.
func (c SecretsScope) IsSecretAllowed(key string) bool {
	// By default set allow access for the secret store.
	var access string = AllowAccess
	// Check and set deny access.
	if strings.EqualFold(c.DefaultAccess, DenyAccess) {
		access = DenyAccess
	}

	// If the allowedSecrets list is not empty then check if the access is specifically allowed for this key.
	if len(c.AllowedSecrets) != 0 {
		return containsKey(c.AllowedSecrets, key)
	}

	// Check key in deny list if deny list is present for the secret store.
	// If the specific key is denied, then alone deny access.
	if deny := containsKey(c.DeniedSecrets, key); deny {
		return !deny
	}

	// Check if defined default access is allow.
	return access == AllowAccess
}

// Runs Binary Search on a sorted list of strings to find a key.
func containsKey(s []string, key string) bool {
	index := sort.SearchStrings(s, key)

	return index < len(s) && s[index] == key
}

// ParseAccessControlSpec creates an in-memory copy of the Access Control Spec for fast lookup
func ParseAccessControlSpec(accessControlSpec AccessControlSpec, protocol string) (*AccessControlList, error) {
	if accessControlSpec.TrustDomain == "" &&
		accessControlSpec.DefaultAction == "" &&
		(accessControlSpec.AppPolicies == nil || len(accessControlSpec.AppPolicies) == 0) {
		// No ACL has been specified
		log.Debugf("No Access control policy specified")
		return nil, nil
	}

	var accessControlList AccessControlList
	accessControlList.PolicySpec = make(map[string]AccessControlListPolicySpec)
	accessControlList.DefaultAction = strings.ToLower(accessControlSpec.DefaultAction)

	accessControlList.TrustDomain = accessControlSpec.TrustDomain
	if accessControlSpec.TrustDomain == "" {
		accessControlList.TrustDomain = DefaultTrustDomain
	}

	accessControlList.DefaultAction = accessControlSpec.DefaultAction
	if accessControlSpec.DefaultAction == "" {
		if accessControlSpec.AppPolicies == nil || len(accessControlSpec.AppPolicies) > 0 {
			// Some app level policies have been specified but not default global action is set. Default to more secure option - Deny
			log.Warnf("No global default action has been specified. Setting default global action as Deny")
			accessControlList.DefaultAction = DenyAccess
		} else {
			// An empty ACL has been specified. Set default global action to Allow
			accessControlList.DefaultAction = AllowAccess
		}
	}

	var invalidTrustDomain []string
	var invalidNamespace []string
	var invalidAppName bool
	accessControlList.PolicySpec = make(map[string]AccessControlListPolicySpec)
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

		operationPolicy := make(map[string]AccessControlListOperationAction)

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

			if protocol == HTTPProtocol {
				operationPrefix = strings.ToLower(operationPrefix)
				operationPostfix = strings.ToLower(operationPostfix)
			}

			operationActions := AccessControlListOperationAction{
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
		aclPolicySpec := AccessControlListPolicySpec{
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
		return nil, errors.New(fmt.Sprintf(
			"invalid access control spec. missing trustdomain for apps: %v, missing namespace for apps: %v, missing app name on at least one of the app policies: %v",
			invalidTrustDomain,
			invalidNamespace,
			invalidAppName))
	}

	return &accessControlList, nil
}

// GetAndParseSpiffeID retrieves the SPIFFE Id from the cert and parses it
func GetAndParseSpiffeID(ctx context.Context) (*SpiffeID, error) {
	spiffeID, err := getSpiffeID(ctx)
	if err != nil {
		return nil, err
	}

	id, err := parseSpiffeID(spiffeID)
	return id, err
}

func parseSpiffeID(spiffeID string) (*SpiffeID, error) {
	if spiffeID == "" {
		return nil, errors.New("input spiffe id string is empty")
	}

	if !strings.HasPrefix(spiffeID, SpiffeIDPrefix) {
		return nil, errors.New(fmt.Sprintf("input spiffe id: %s is invalid", spiffeID))
	}

	// The SPIFFE Id will be of the format: spiffe://<trust-domain/ns/<namespace>/<app-id>
	parts := strings.Split(spiffeID, "/")
	if len(parts) < 6 {
		return nil, errors.New(fmt.Sprintf("input spiffe id: %s is invalid", spiffeID))
	}

	var id SpiffeID
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
						if strings.HasPrefix(spiffeID, SpiffeIDPrefix) {
							return spiffeID, nil
						}
					}
				}
			}
		}
	}

	return "", nil
}

// IsOperationAllowedByAccessControlPolicy determines if access control policies allow the operation on the target app
func IsOperationAllowedByAccessControlPolicy(spiffeID *SpiffeID, srcAppID string, inputOperation string, httpVerb common.HTTPExtension_Verb, appProtocol string, accessControlList *AccessControlList) (bool, string) {
	if accessControlList == nil {
		// No access control list is provided. Do nothing
		return isActionAllowed(AllowAccess), ""
	}

	action := accessControlList.DefaultAction
	actionPolicy := ActionPolicyGlobal

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
		actionPolicy = ActionPolicyApp
	}

	// the in-memory table has operations stored in the format "/operation name".
	// Prepend a "/" in case missing so that the match works
	if !strings.HasPrefix(inputOperation, "/") {
		inputOperation = "/" + inputOperation
	}

	inputOperationPrefix, inputOperationPostfix := getOperationPrefixAndPostfix(inputOperation)

	// If HTTP, make case-insensitive
	if appProtocol == HTTPProtocol {
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
		if appProtocol == HTTPProtocol {
			if httpVerb != common.HTTPExtension_NONE {
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
		} else if appProtocol == GRPCProtocol {
			// No http verb match is needed.
			action = operationPolicy.OperationAction
		}
	}

	return isActionAllowed(action), actionPolicy
}

func isActionAllowed(action string) bool {
	return strings.EqualFold(action, AllowAccess)
}

func getKeyForAppID(appID, namespace string) string {
	key := appID + "||" + namespace
	return key
}

// getOperationPrefixAndPostfix returns an app operation prefix and postfix
// The prefix can be stored in the in-memory ACL for fast lookup
// e.g.: /invoke/*, prefix = /invoke, postfix = /*
func getOperationPrefixAndPostfix(operation string) (string, string) {
	operationParts := strings.Split(operation, "/")
	operationPrefix := "/" + operationParts[1]
	operationPostfix := "/" + strings.Join(operationParts[2:], "/")

	return operationPrefix, operationPostfix
}
