// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package vault

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"

	"golang.org/x/net/http2"

	"github.com/dapr/components-contrib/secretstores"
)

const (
	defaultVaultAddress          string = "https://127.0.0.1:8200"
	componentVaultAddress        string = "vaultAddr"
	componentCaCert              string = "caCert"
	componentCaPath              string = "caPath"
	componentCaPem               string = "caPem"
	componentSkipVerify          string = "skipVerify"
	componentTLSServerName       string = "tlsServerName"
	componentVaultTokenMountPath string = "vaultTokenMountPath"
	componentVaultKVPrefix       string = "vaultKVPrefix"
	defaultVaultKVPrefix         string = "dapr"
	vaultHTTPHeader              string = "X-Vault-Token"
)

// vaultSecretStore is a secret store implementation for HashiCorp Vault
type vaultSecretStore struct {
	client              *http.Client
	vaultAddress        string
	vaultTokenMountPath string
	vaultKVPrefix       string
}

// tlsConfig is TLS configuration to interact with HashiCorp Vault
type tlsConfig struct {
	vaultCAPem      string
	vaultCACert     string
	vaultCAPath     string
	vaultSkipVerify bool
	vaultServerName string
}

// vaultKVResponse is the response data from Vault KV.
type vaultKVResponse struct {
	Data struct {
		Data map[string]string `json:"data"`
	} `json:"data"`
}

// NewHashiCorpVaultSecretStore returns a new HashiCorp Vault secret store
func NewHashiCorpVaultSecretStore() secretstores.SecretStore {
	return &vaultSecretStore{
		client: &http.Client{},
	}
}

// Init creates a HashiCorp Vault client
func (v *vaultSecretStore) Init(metadata secretstores.Metadata) error {
	props := metadata.Properties

	// Get Vault address
	address := props[componentVaultAddress]
	if address == "" {
		v.vaultAddress = defaultVaultAddress
	}

	v.vaultAddress = address

	// Generate TLS config
	tlsConf := metadataToTLSConfig(props)

	client, err := v.createHTTPClient(tlsConf)
	if err != nil {
		return fmt.Errorf("couldn't create client using config: %s", err)
	}

	v.client = client

	tokenMountPath := props[componentVaultTokenMountPath]
	if tokenMountPath == "" {
		return fmt.Errorf("token mount path not set")
	}

	v.vaultTokenMountPath = tokenMountPath

	vaultKVPrefix := props[componentVaultKVPrefix]
	if vaultKVPrefix == "" {
		vaultKVPrefix = defaultVaultKVPrefix
	}

	v.vaultKVPrefix = vaultKVPrefix

	return nil
}

func metadataToTLSConfig(props map[string]string) *tlsConfig {
	tlsConf := tlsConfig{}

	// Configure TLS settings
	skipVerify := props[componentSkipVerify]
	tlsConf.vaultSkipVerify = false
	if skipVerify == "true" {
		tlsConf.vaultSkipVerify = true
	}

	tlsConf.vaultCACert = props[componentCaCert]
	tlsConf.vaultCAPem = props[componentCaPem]
	tlsConf.vaultCAPath = props[componentCaPath]
	tlsConf.vaultServerName = props[componentTLSServerName]

	return &tlsConf
}

// GetSecret retrieves a secret using a key and returns a map of decrypted string/string values
func (v *vaultSecretStore) GetSecret(req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
	token, err := v.readVaultToken()
	if err != nil {
		return secretstores.GetSecretResponse{Data: nil}, err
	}

	// Create get secret url
	// TODO: Add support for versioned secrets when the secretstore request has support for it
	vaultSecretPathAddr := fmt.Sprintf("%s/v1/secret/data/%s/%s?version=0", v.vaultAddress, v.vaultKVPrefix, req.Name)

	httpReq, err := http.NewRequest(http.MethodGet, vaultSecretPathAddr, nil)
	// Set vault token.
	httpReq.Header.Set(vaultHTTPHeader, token)
	if err != nil {
		return secretstores.GetSecretResponse{Data: nil}, fmt.Errorf("couldn't generate request: %s", err)
	}

	httpresp, err := v.client.Do(httpReq)
	if err != nil {
		return secretstores.GetSecretResponse{Data: nil}, fmt.Errorf("couldn't get secret: %s", err)
	}

	defer httpresp.Body.Close()

	if httpresp.StatusCode != 200 {
		var b bytes.Buffer
		io.Copy(&b, httpresp.Body)
		return secretstores.GetSecretResponse{Data: nil}, fmt.Errorf("couldn't to get successful response: %#v, %s",
			httpresp, b.String())
	}

	var d vaultKVResponse

	if err := json.NewDecoder(httpresp.Body).Decode(&d); err != nil {
		return secretstores.GetSecretResponse{Data: nil}, fmt.Errorf("couldn't decode response body: %s", err)
	}

	resp := secretstores.GetSecretResponse{
		Data: map[string]string{},
	}

	// Only using secret data and ignore metadata
	// TODO: add support for metadata response when secretstores support it.
	for k, v := range d.Data.Data {
		resp.Data[k] = v
	}

	return resp, nil
}

func (v *vaultSecretStore) readVaultToken() (string, error) {
	data, err := ioutil.ReadFile(v.vaultTokenMountPath)
	if err != nil {
		return "", fmt.Errorf("couldn't read vault token: %s", err)
	}

	return string(bytes.TrimSpace(data)), nil
}

func (v *vaultSecretStore) createHTTPClient(config *tlsConfig) (*http.Client, error) {
	rootCAPools, err := v.getRootCAsPools(config.vaultCAPem, config.vaultCAPath, config.vaultCACert)
	if err != nil {
		return nil, err
	}

	tlsClientConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    rootCAPools,
	}

	if config.vaultSkipVerify {
		tlsClientConfig.InsecureSkipVerify = true
	}

	if config.vaultServerName != "" {
		tlsClientConfig.ServerName = config.vaultServerName
	}

	// Setup http transport
	transport := &http.Transport{
		TLSClientConfig: tlsClientConfig,
	}

	// Configure http2 client
	err = http2.ConfigureTransport(transport)
	if err != nil {
		return nil, errors.New("failed to configure http2")
	}

	return &http.Client{
		Transport: transport,
	}, nil
}

// getRootCAsPools returns root CAs when you give it CA Pem file, CA path, and CA Certificate. Default is system certificates.
func (v *vaultSecretStore) getRootCAsPools(vaultCAPem string, vaultCAPath string, vaultCACert string) (*x509.CertPool, error) {
	if vaultCAPem != "" {
		certPool := x509.NewCertPool()
		cert := []byte(vaultCAPem)
		if ok := certPool.AppendCertsFromPEM(cert); !ok {
			return nil, fmt.Errorf("couldn't read PEM")
		}
		return certPool, nil
	}

	if vaultCAPath != "" {
		certPool := x509.NewCertPool()
		if err := readCertificateFolder(certPool, vaultCAPath); err != nil {
			return nil, err
		}
		return certPool, nil
	}

	if vaultCACert != "" {
		certPool := x509.NewCertPool()
		if err := readCertificateFile(certPool, vaultCACert); err != nil {
			return nil, err
		}
		return certPool, nil
	}

	certPool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("couldn't read system certs: %s", err)
	}

	return certPool, err
}

// readCertificateFile reads the certificate at given path
func readCertificateFile(certPool *x509.CertPool, path string) error {
	// Read certificate file
	pemFile, err := ioutil.ReadFile(path)
	if err != nil {
		return fmt.Errorf("couldn't read CA file from disk: %s", err)
	}

	if ok := certPool.AppendCertsFromPEM(pemFile); !ok {
		return fmt.Errorf("couldn't read PEM")
	}

	return nil
}

// readCertificateFolder scans a folder for certificates
func readCertificateFolder(certPool *x509.CertPool, path string) error {
	err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}

		return readCertificateFile(certPool, p)
	})

	if err != nil {
		return fmt.Errorf("couldn't read certificates at %s: %s", path, err)
	}
	return nil
}
