# Dapr 1.16.19

This update contains the following bug fix:
- [Azure component authentication halting at the SPIFFE credential instead of falling back](#azure-component-authentication-halting-at-the-spiffe-credential-instead-of-falling-back)

## Azure component authentication halting at the SPIFFE credential instead of falling back

### Problem

Azure (Microsoft Entra ID) components authenticate by trying a chain of credentials in order until one succeeds.
When `azureClientId` and `azureTenantId` were set, the chain included the SPIFFE workload identity credential, and if no SPIFFE JWT SVID source was available the chain stopped at that step:

```
ChainedTokenCredential: failed to acquire a token.
Attempted credentials:
	ClientAssertionCredential: failed to get JWT SVID source from context
```

Credentials later in the chain, such as managed identity or the Azure CLI, were never attempted, so the component failed to authenticate even though a working credential was available.

### Impact

You were affected on Dapr 1.16.0 or later (where the SPIFFE credential joined the default chain) in either of these configurations:

- An Azure component with `azureClientId` and `azureTenantId` set but no client secret or certificate, relying on a later credential in the default chain (for example managed identity or the Azure CLI).
- An explicit `azureAuthMethods` list placing `spiffeworkloadidentity` before another method (for example `spiffeworkloadidentity,managedidentity`), expecting fallback when SPIFFE is not configured.

### Root Cause

`ChainedTokenCredential` only continues past a credential that reports a `credentialUnavailableError`; any other error is treated as fatal and ends the chain.
The SPIFFE credential returned a plain error when the context carried no JWT SVID source, so a missing prerequisite was treated as a fatal authentication failure rather than a signal to try the next credential.

### Solution

The SPIFFE credential now reports itself as unavailable when no JWT SVID source is present, before any token request is made, which is exactly what `ChainedTokenCredential` requires to continue to the next credential in the chain.
Both the default chain and explicitly ordered `azureAuthMethods` lists now fall through as expected, and behavior when a SPIFFE source is configured is unchanged.
