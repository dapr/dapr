// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package keyvault

import (
	"crypto/rsa"
	"crypto/x509"
	"fmt"

	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/adal"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"golang.org/x/crypto/pkcs12"
)

// ClientAuthorizer provides the options to get a bearer authorizer from a client certificate.
type ClientAuthorizer struct {
	*auth.ClientCertificateConfig
	CertificateData []byte
}

// NewClientAuthorizer creates an ClientAuthorizer object configured to obtain an Authorizer through Client Credentials.
func NewClientAuthorizer(certificatePath string, certificateBytes []byte, certificatePassword string, clientID string, tenantID string) ClientAuthorizer {
	return ClientAuthorizer{
		&auth.ClientCertificateConfig{
			CertificatePath:     certificatePath,
			CertificatePassword: certificatePassword,
			ClientID:            clientID,
			TenantID:            tenantID,
			Resource:            azure.PublicCloud.ResourceIdentifiers.KeyVault,
			AADEndpoint:         azure.PublicCloud.ActiveDirectoryEndpoint,
		},
		certificateBytes,
	}
}

// Authorizer gets an authorizer object from client certificate.
func (c ClientAuthorizer) Authorizer() (autorest.Authorizer, error) {
	if c.ClientCertificateConfig.CertificatePath != "" {
		// in standalone mode, component yaml will pass cert path
		return c.ClientCertificateConfig.Authorizer()
	} else if len(c.CertificateData) > 0 {
		// in kubernetes mode, runtime will get the secret from K8S secret store and pass byte array
		spToken, err := c.ServicePrincipalTokenByCertBytes()
		if err != nil {
			return nil, fmt.Errorf("failed to get oauth token from certificate auth: %v", err)
		}
		return autorest.NewBearerAuthorizer(spToken), nil
	}

	return nil, fmt.Errorf("certificate is not given")
}

// ServicePrincipalTokenByCertBytes gets the service principal token by CertificateBytes.
func (c ClientAuthorizer) ServicePrincipalTokenByCertBytes() (*adal.ServicePrincipalToken, error) {
	oauthConfig, err := adal.NewOAuthConfig(c.AADEndpoint, c.TenantID)
	if err != nil {
		return nil, err
	}

	certificate, rsaPrivateKey, err := c.decodePkcs12(c.CertificateData, c.CertificatePassword)
	if err != nil {
		return nil, fmt.Errorf("failed to decode pkcs12 certificate while creating spt: %v", err)
	}
	return adal.NewServicePrincipalTokenFromCertificate(*oauthConfig, c.ClientID, certificate, rsaPrivateKey, c.Resource)
}

func (c ClientAuthorizer) decodePkcs12(pkcs []byte, password string) (*x509.Certificate, *rsa.PrivateKey, error) {
	privateKey, certificate, err := pkcs12.Decode(pkcs, password)
	if err != nil {
		return nil, nil, err
	}

	rsaPrivateKey, isRsaKey := privateKey.(*rsa.PrivateKey)
	if !isRsaKey {
		return nil, nil, fmt.Errorf("PKCS#12 certificate must contain an RSA private key")
	}

	return certificate, rsaPrivateKey, nil
}
