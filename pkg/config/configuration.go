// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
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

	"github.com/dapr/dapr/pkg/logger"
	"github.com/dapr/dapr/pkg/proto/common/v1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"github.com/pkg/errors"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	yaml "gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/util/sets"
)

var log = logger.NewLogger("dapr.configuration")

const (
	operatorCallTimeout = time.Second * 5
	operatorMaxRetries  = 100
	AllowAccess         = "allow"
	DenyAccess          = "deny"
	// AccessControlActionAllow defines the allow action for an operation
	AccessControlActionAllow = "allow"
	// AccessControlActionDeny defines the deny action for an operation
	AccessControlActionDeny = "deny"
	DefaultTrustDomain      = "public"
	DefaultNamespace        = "default"
)

type Configuration struct {
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
}

type ConfigurationSpec struct {
	HTTPPipelineSpec  PipelineSpec      `json:"httpPipeline,omitempty" yaml:"httpPipeline,omitempty"`
	TracingSpec       TracingSpec       `json:"tracing,omitempty" yaml:"tracing,omitempty"`
	MTLSSpec          MTLSSpec          `json:"mtls,omitempty"`
	MetricSpec        MetricSpec        `json:"metric,omitempty" yaml:"metric,omitempty"`
	Secrets           SecretsSpec       `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	AccessControlSpec AccessControlSpec `json:"accessControl,omitempty" yaml:"accessControl,omitempty"`
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
	SamplingRate string `json:"samplingRate" yaml:"samplingRate"`
	Stdout       bool   `json:"stdout" yaml:"stdout"`
}

// MetricSpec configuration for metrics
type MetricSpec struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

// AppPolicySpec defines the policy data structure for each app
type AppPolicySpec struct {
	AppName             string         `json:"app" yaml:"app"`
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
	fmt.Println("@@@@ Loading default config...")
	return &Configuration{
		Spec: ConfigurationSpec{
			TracingSpec: TracingSpec{
				SamplingRate: "",
			},
			MetricSpec: MetricSpec{
				Enabled: true,
			},
			AccessControlSpec: AccessControlSpec{
				DefaultAction: "allow",
				TrustDomain:   "public",
			},
		},
	}
}

// LoadStandaloneConfiguration gets the path to a config file and loads it into a configuration
func LoadStandaloneConfiguration(config string) (*Configuration, error) {
	_, err := os.Stat(config)
	if err != nil {
		return nil, err
	}

	b, err := ioutil.ReadFile(config)
	if err != nil {
		return nil, err
	}

	var conf Configuration
	err = yaml.Unmarshal(b, &conf)
	if err != nil {
		return nil, err
	}
	err = sortAndValidateSecretsConfiguration(&conf)
	if err != nil {
		return nil, err
	}

	return &conf, nil
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
	var conf Configuration
	err = json.Unmarshal(resp.GetConfiguration(), &conf)
	if err != nil {
		return nil, err
	}

	err = sortAndValidateSecretsConfiguration(&conf)
	if err != nil {
		return nil, err
	}

	return &conf, nil
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
func ParseAccessControlSpec(accessControlSpec AccessControlSpec) (AccessControlList, error) {
	var accessControlList AccessControlList
	accessControlList.PolicySpec = make(map[string]AccessControlListPolicySpec)
	accessControlList.DefaultAction = strings.ToLower(accessControlSpec.DefaultAction)

	if accessControlSpec.TrustDomain != "" {
		fmt.Println("@@@@@@ TranslateAccessControlSpec - Trust domain: ")
		accessControlList.TrustDomain = accessControlSpec.TrustDomain
	} else {
		fmt.Println("@@@@@@ TranslateAccessControlSpec - Default domain: ")
		accessControlList.TrustDomain = DefaultTrustDomain
	}

	var log = logger.NewLogger("dapr.configuration")
	log.Infof("@@@@@ Translating policy spec....")
	var invalidTrustDomain []string
	var invalidNamespace []string
	accessControlList.PolicySpec = make(map[string]AccessControlListPolicySpec)
	for _, appPolicySpec := range accessControlSpec.AppPolicies {
		invalid := false
		if appPolicySpec.TrustDomain == "" {
			invalidTrustDomain = append(invalidTrustDomain, appPolicySpec.AppName)
			invalid = true
		}
		if appPolicySpec.Namespace == "" {
			invalidNamespace = append(invalidNamespace, appPolicySpec.AppName)
			invalid = true
		}

		if invalid {
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

			operationActions := AccessControlListOperationAction{
				OperationPostFix: operationPostfix,
				VerbAction:       make(map[string]string),
			}

			// Iterate over all the http verbs and create a map and set the action for fast lookup
			for _, verb := range appPolicy.HTTPVerb {
				operationActions.VerbAction[verb] = appPolicy.Action
			}

			operationPolicy[operationPrefix] = operationActions
		}
		aclPolicySpec := AccessControlListPolicySpec{
			AppName:             appPolicySpec.AppName,
			DefaultAction:       appPolicySpec.DefaultAction,
			TrustDomain:         appPolicySpec.TrustDomain,
			Namespace:           appPolicySpec.Namespace,
			AppOperationActions: operationPolicy,
		}
		accessControlList.PolicySpec[aclPolicySpec.AppName] = aclPolicySpec
	}

	if len(invalidTrustDomain) > 0 || len(invalidNamespace) > 0 {
		err := fmt.Errorf("Invalid Access Control Spec. %s, %s",
			fmt.Sprintf("Missing TrustDomain for apps: %v", invalidTrustDomain),
			fmt.Sprintf("Missing Namespace for apps: %v", invalidNamespace))
		return accessControlList, err
	}

	return accessControlList, nil
}

// TryGetAndParseSpiffeID retrieves the SPIFFE Id from the cert and parses it
func TryGetAndParseSpiffeID(ctx context.Context) (*SpiffeID, error) {
	// TODO: Remove hardcoding for testing
	spiffeID, err := getSpiffeID(ctx)
	if err != nil {
		return nil, err
	}

	// spiffeID = "spiffe://a/ns/b/pythonapp"
	log.Infof("spiffe id :- %v\n", spiffeID)
	id := parseSpiffeID(spiffeID)
	return id, nil
}

func parseSpiffeID(spiffeID string) *SpiffeID {
	if spiffeID == "" {
		log.Infof("Input spiffe id string is empty")
		return nil
	}
	log.Infof("input spiffe id string :- %v\n", spiffeID)

	// The SPIFFE Id will be of the format: spiffe://<trust-domain/ns/<namespace>/<app-id>
	parts := strings.Split(spiffeID, "/")
	var id SpiffeID
	id.TrustDomain = parts[2]
	id.Namespace = parts[4]
	id.AppID = parts[5]

	return &id
}

func getSpiffeID(ctx context.Context) (string, error) {

	var spiffeID string
	fmt.Println("@@@@ call arrived")
	peer, ok := peer.FromContext(ctx)
	if ok {
		tlsInfo := peer.AuthInfo.(credentials.TLSInfo)
		fmt.Println(len(tlsInfo.State.PeerCertificates))

		oid := asn1.ObjectIdentifier{2, 5, 29, 17}

		for _, crt := range tlsInfo.State.PeerCertificates {
			for _, ext := range crt.Extensions {
				if ext.Id.Equal(oid) {
					fmt.Println("@@@@ OID found")

					var sequence asn1.RawValue
					if rest, err := asn1.Unmarshal(ext.Value, &sequence); err != nil {
						fmt.Println(err)
						continue
					} else if len(rest) != 0 {
						fmt.Println("the SAN extension is incorrectly encoded")
						continue
					}

					if !sequence.IsCompound || sequence.Tag != asn1.TagSequence || sequence.Class != asn1.ClassUniversal {
						fmt.Println("the SAN extension is incorrectly encoded")
						continue
					}

					for bytes := sequence.Bytes; len(bytes) > 0; {
						var rawValue asn1.RawValue
						var err error

						bytes, err = asn1.Unmarshal(bytes, &rawValue)
						if err != nil {
							return "", err
						}

						fmt.Println("@@@@ Value: " + string(rawValue.Bytes))
						spiffeID = string(rawValue.Bytes)
						if strings.HasPrefix(spiffeID, "spiffe://") {
							fmt.Printf("Found spiffe id: %s\n", spiffeID)
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
func IsOperationAllowedByAccessControlPolicy(spiffeID *SpiffeID, srcAppID string, inputOperation string, httpVerb common.HTTPExtension_Verb, accessControlList *AccessControlList) (bool, string) {

	if accessControlList == nil {
		// No access control list is provided. Do nothing
		return true, ""
	}

	log.Infof("@@@@ Dumping all policy specs....")
	log.Infof("Default action: %s, TrustDomain: %s", accessControlList.DefaultAction, accessControlList.TrustDomain)
	for key, spec := range accessControlList.PolicySpec {
		log.Infof("key: %s, value: %s", key, spec)
	}

	log.Infof("Checking access control policy for invocation by %v, operation: %v, httpVerb: %v", srcAppID, inputOperation, httpVerb)
	action := accessControlList.DefaultAction

	if srcAppID == "" {
		return isActionAllowed(action), fmt.Sprintf("Unable to find policy spec for srcAppId: %s. Applying default action", srcAppID)
	}

	appPolicy, found := accessControlList.PolicySpec[srcAppID]

	if !found {
		return isActionAllowed(action), fmt.Sprintf("Unable to find policy spec for srcAppId: %s. Applying default action", srcAppID)
	}

	log.Infof("@@@@ Using policy spec for srcAppId: %s: %v", srcAppID, appPolicy)
	var logMessage string
	if spiffeID == nil {
		logMessage = fmt.Sprintf("Unable to verify spiffe id of the client. Will apply default global action")
		return isActionAllowed(action), ""
	}

	log.Infof("@@@@ App policy spec found. Trying to match policy spec for srcAppId: %s: %v", srcAppID, appPolicy)

	// Match trust domain
	if appPolicy.TrustDomain != spiffeID.TrustDomain {
		return isActionAllowed(action), fmt.Sprintf("Trust Domain mismatch. Apply global default action")
	}

	// Match namespace
	if appPolicy.Namespace != spiffeID.Namespace {
		return isActionAllowed(action), fmt.Sprintf("Namespace mismatch. Apply global default action")
	}

	if appPolicy.DefaultAction != "" {
		// This point onwards, default action is the default action specified in the spec for the app
		action = appPolicy.DefaultAction
	}

	// The acl may specify the operation in a format invoke/*, get and match only the prefix first

	if !strings.HasPrefix(inputOperation, "/") {
		inputOperation = "/" + inputOperation
	}

	inputOperationPrefix, inputOperationPostfix := getOperationPrefixAndPostfix(inputOperation)

	// Check the operation specific policy
	// for operation, policyOperation := range policy.AppOperationActions {
	operationPolicy, found := appPolicy.AppOperationActions[inputOperationPrefix]
	if found {
		// Match postfix
		if operationPolicy.OperationPostFix != "/*" && !strings.HasPrefix(operationPolicy.OperationPostFix, inputOperationPostfix) {
			logMessage = fmt.Sprintf("Could not match operation post fix. Applying default action for srcAppId: %s operation: %v, verb: %v action: %v", srcAppID, inputOperation, httpVerb, action)
			return isActionAllowed(action), logMessage
		}

		log.Infof("Found operation: %v. checking http verbs", inputOperation)
		if httpVerb != common.HTTPExtension_NONE {
			verbAction, found := operationPolicy.VerbAction[httpVerb.String()]
			if found {
				action = verbAction
				logMessage = fmt.Sprintf("Applying action for srcAppId: %s operation: %v, verb: %v action: %v", srcAppID, inputOperation, httpVerb, action)
				// break
			} else {
				verbAction, found = operationPolicy.VerbAction["*"]
				if found {
					action = verbAction
					logMessage = fmt.Sprintf("Matched wildcard httpverb. Applying action for srcAppId: %s operation: %v, verb: %v action: %v", srcAppID, inputOperation, httpVerb, action)
				}
			}
		} else {
			logMessage = fmt.Sprintf("Http Verb not specified. Applying default action for the app: %v for operation: %v", action, inputOperation)
			action = appPolicy.DefaultAction
		}
	}

	if logMessage == "" {
		log.Infof("Applying access control policy action: %v", action)
	}
	return isActionAllowed(action), logMessage
}

func isActionAllowed(action string) bool {
	if strings.ToLower(action) == AccessControlActionAllow {
		return true
	}
	return false
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
