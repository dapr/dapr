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
	"reflect"
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
)

type Configuration struct {
	Spec ConfigurationSpec `json:"spec" yaml:"spec"`
}

// AccessControlList is an in-memory access control list config for fast lookup
type AccessControlList struct {
	DefaultAction string
	TrustDomain   string
	PolicySpec    map[string]AppPolicySpec
}

type ConfigurationSpec struct {
	HTTPPipelineSpec PipelineSpec `json:"httpPipeline,omitempty" yaml:"httpPipeline,omitempty"`
	TracingSpec      TracingSpec  `json:"tracing,omitempty" yaml:"tracing,omitempty"`
	MTLSSpec         MTLSSpec     `json:"mtls,omitempty"`
	MetricSpec       MetricSpec   `json:"metric,omitempty" yaml:"metric,omitempty"`
	Secrets          SecretsSpec  `json:"secrets,omitempty" yaml:"secrets,omitempty"`
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
	trustDomain string
	namespace   string
	appID       string
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

// TranslateAccessControlSpec creates an in-memory copy of the Access Control Spec for fast lookup
func TranslateAccessControlSpec(accessControlSpec AccessControlSpec) AccessControlList {
	var accessControlList AccessControlList
	accessControlList.PolicySpec = make(map[string]AppPolicySpec)
	accessControlList.DefaultAction = strings.ToLower(accessControlSpec.DefaultAction)

	if accessControlSpec.TrustDomain != "" {
		accessControlList.TrustDomain = accessControlSpec.TrustDomain
	} else {

		accessControlList.TrustDomain = DefaultTrustDomain
	}

	var log = logger.NewLogger("dapr.configuration")
	log.Infof("@@@@@ Translating policy spec....")
	log.Infof("@@@@@@ global default action: %s, trust domain: %s", accessControlList.DefaultAction, accessControlList.TrustDomain)
	for _, appPolicySpec := range accessControlSpec.AppPolicies {
		log.Infof("@@@@@ name: %s spec: %s", appPolicySpec.AppName, appPolicySpec)
		accessControlList.PolicySpec[appPolicySpec.AppName] = appPolicySpec
	}

	return accessControlList
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
	id.trustDomain = parts[2]
	id.namespace = parts[4]
	id.appID = parts[5]

	return &id
}

func getSpiffeID(ctx context.Context) (string, error) {
	peer, ok := peer.FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("could not retrieve spiffe id from the grpc context")
	}

	log.Info(peer)

	if peer.AuthInfo == nil {
		return "", fmt.Errorf("could not retrieve auth info from grpc context tls info")
	}

	tlsInfo := peer.AuthInfo.(credentials.TLSInfo)

	log.Infof("@@@@ Peer TLS info: %v", tlsInfo)

	if tlsInfo.State.HandshakeComplete == false {
		return "", fmt.Errorf("tls handshake is not complete")
	}

	certChain := tlsInfo.State.VerifiedChains
	t := reflect.TypeOf(certChain)
	fmt.Println(t)
	if certChain == nil || len(certChain[0]) == 0 {
		return "", fmt.Errorf("could not retrieve read client cert info")
	}

	log.Infof("@@@@@ Dump certChain[0][0].Extensions[0].Value: %v", certChain[0][0].Extensions[0].Value)
	oidSubjectAlternativeName := asn1.ObjectIdentifier{2, 5, 29, 17}
	var spiffeID string
	for i, ext := range certChain[0][0].Extensions {
		log.Infof("@@@@ Dumping extension: %v: %v", i, ext)
		if ext.Id.Equal(oidSubjectAlternativeName) {
			log.Infof("Matched oidSubjectAlternativeName")
			var sequence asn1.RawValue
			_, _ = asn1.Unmarshal(ext.Value, &sequence)

			for bytes := sequence.Bytes; len(bytes) > 0; {
				var rawValue asn1.RawValue

				bytes, _ = asn1.Unmarshal(bytes, &rawValue)

				id := string(rawValue.Bytes)
				log.Infof("id: %s", id)
				log.Infof("id slice string: %s", string(rawValue.Bytes[:]))
				if strings.HasPrefix(id, "spiffe://") {
					log.Infof("Found spiffe id in cert: %s", id)
					spiffeID = id
				}
			}
		}
	}

	// spiffeID := string(certChain[0][0].Extensions[0].Value[:])

	return spiffeID, nil
}

// IsOperationAllowedByAccessControlPolicy determines if access control policies allow the operation on the target app
func IsOperationAllowedByAccessControlPolicy(spiffeID *SpiffeID, srcAppID string, operation string, httpVerb common.HTTPExtension_Verb, accessControlList *AccessControlList) (bool, string) {

	if accessControlList == nil {
		// No access control list is provided. Do nothing
		return true, ""
	}

	log.Infof("@@@@ Dumping all policy specs....")
	log.Infof("Default action: %s, TrustDomain: %s", accessControlList.DefaultAction, accessControlList.TrustDomain)
	for key, spec := range accessControlList.PolicySpec {
		log.Infof("key: %s, value: %s", key, spec)
	}
	log.Infof("Checking access control policy for invocation by %v, operation: %v, httpVerb: %v", srcAppID, operation, httpVerb)
	action := accessControlList.DefaultAction

	if srcAppID == "" {
		return isActionAllowed(action), fmt.Sprintf("Unable to find policy spec for srcAppId: %s. Applying default action", srcAppID)
	}

	policy, found := accessControlList.PolicySpec[srcAppID]
	log.Infof("@@@@ Using policy spec for srcAppId: %s: %v", srcAppID, policy)

	if !found {
		return isActionAllowed(action), fmt.Sprintf("Unable to find policy spec for srcAppId: %s. Applying default action", srcAppID)
	}

	var logMessage string
	if spiffeID == nil {
		logMessage = fmt.Sprintf("Unable to verify spiffe id of the client. Will apply default global action")
	} else {
		action = policy.DefaultAction

		if policy.TrustDomain != "*" && policy.TrustDomain != spiffeID.trustDomain {
			return isActionAllowed(action), fmt.Sprintf("Trust Domain mismatch. Apply global default action")
		}

		// TODO: Check namespace if needed

		inputOperation := "/" + operation
		// Check the operation specific policy
		for _, policyOperation := range policy.AppOperationActions {
			if strings.HasPrefix(policyOperation.Operation, inputOperation) {
				log.Infof("Found operation: %v. checking http verbs", inputOperation)
				if httpVerb != common.HTTPExtension_NONE {
					for _, policyVerb := range policyOperation.HTTPVerb {
						if policyVerb == httpVerb.String() || policyVerb == "*" {
							action = policyOperation.Action
							logMessage = fmt.Sprintf("Applying action for srcAppId: %s operation: %v, verb: %v action: %v", srcAppID, inputOperation, policyVerb, action)
							break
						}
					}
				} else {
					logMessage = fmt.Sprintf("Http Verb not specified. Applying action: %v for operation: %v", action, inputOperation)
					action = policyOperation.Action
				}
			}
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
