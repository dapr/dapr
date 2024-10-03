# Dapr E2E Test Infrastructure

## Runbooks

### Renew Azure Login

The Azure credential must be renewed every 6 months. When the credential expire, you will see the following error in the `Login to Azure` step when running E2E tests on Azure:

```txt
Run azure/login@v1
Running Azure CLI Login.
/usr/bin/az cloud set -n azurecloud
Done setting cloud: "azurecloud"
Note: Azure/login action also supports OIDC login mechanism. Refer https://github.com/azure/login#configure-a-service-principal-with-a-federated-credential-to-use-oidc-based-authentication for more details.
Attempting Azure CLI login by using service principal with secret...
Error: AADSTS7000222: The provided client secret keys for app '***' are expired. Visit the Azure portal to create new keys for your app: https://aka.ms/NewClientSecret, or consider using certificate credentials for added security: https://aka.ms/certCreds. Trace ID: 4dc95b69-351d-438c-900c-62f611732901 Correlation ID: 7c6976b3-b878-42cc-bd40-71404c449ef7 Timestamp: 2024-07-01 16:04:07Z

Error: Interactive authentication is needed. Please run:
az login

Error: Login failed with Error: The process '/usr/bin/az' failed with exit code 1. Double check if the 'auth-type' is correct. Refer to https://github.com/Azure/login#readme for more information.
```

To fix this, you will need the following:
- Access to the dapr@dapr.io credential in the Dapr's 1Password
- Admin access to the Dapr's GitHub organization.

Steps to fix it:
- Go to https://portal.azure.com and login with the `dapr@dapr.io` credential.
- Navigate to `Microsoft Entra ID` -> `App Registrations` -> `All applications` -> `Dapr E2E Tests` -> `Certificates and Secrets`
- Copy the entire description of any of the previous secrets, it looks like a JSON payload.
- Click on `New client secret`, paste the JSON payload into the description and keep the recommended expiration of 180 days.
- Don't close the window yet. Now, open a text editor and compose the following JSON payload (single line):
```json
{ "clientId": "<already present>", "clientSecret": "PASTE CLIENT SECRET HERE", "subscriptionId": "<already present>", "tenantId": "<already present>" }
```
- Click on the clipboard icon next to the value of the secret on Azure Portal and paste it in `PASTE CLIENT SECRET HERE`.
- Now, let's set the JSON payload as a GitHub secret here: https://github.com/dapr/dapr/settings/secrets/actions/AZURE_CREDENTIALS
- Copy-and-paste the JSON payload from above into the text box. Keep it as a single line.
- Finally, click on `Update secret`
