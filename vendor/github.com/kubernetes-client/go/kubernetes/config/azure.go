package config

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/Azure/go-autorest/autorest/adal"
)

func (l *KubeConfigLoader) loadAzureToken() bool {
	if l.user.AuthProvider == nil || l.user.AuthProvider.Name != "azure" {
		return false
	}

	// TODO refresh token if needed here.
	if l.user.AuthProvider.Config != nil {
		expires, exists := l.user.AuthProvider.Config["expires-on"]
		if exists {
			ts, err := strconv.ParseInt(expires, 10, 64)
			if err != nil {
				expiry := time.Unix(ts, 0)
				if time.Now().After(expiry) {
					if err := l.refreshAzureToken(); err != nil {
						log.Printf("Error refreshing token: %v", err)
					}
				}
			}
		}
	}

	// Use AAD access token
	if l.user.AuthProvider.Config != nil {
		token, exists := l.user.AuthProvider.Config["access-token"]
		if exists {
			l.restConfig.token = "Bearer " + token
			return true
		}
	}
	return false
}

func (l *KubeConfigLoader) refreshAzureToken() error {
	tenantID, exists := l.user.AuthProvider.Config["tenant-id"]
	if !exists {
		return fmt.Errorf("Missing tenant id in authProvider.config")
	}
	clientID, exists := l.user.AuthProvider.Config["client-id"]
	if !exists {
		return fmt.Errorf("Missing client id!")
	}

	aadEndpoint := "https://login.microsoftonline.com"
	config, err := adal.NewOAuthConfig(aadEndpoint, tenantID)
	if err != nil {
		return err
	}
	resource := aadEndpoint + "/" + tenantID
	token := adal.Token{
		AccessToken:  l.user.AuthProvider.Config["access-token"],
		RefreshToken: l.user.AuthProvider.Config["refresh-token"],
		ExpiresIn:    json.Number(l.user.AuthProvider.Config["expires-in"]),
		ExpiresOn:    json.Number(l.user.AuthProvider.Config["expires-on"]),
	}
	sptToken, err := adal.NewServicePrincipalTokenFromManualToken(*config, clientID, resource, token)
	if err := sptToken.Refresh(); err != nil {
		return err
	}
	l.user.AuthProvider.Config["access-token"] = sptToken.OAuthToken()
	return nil
}
