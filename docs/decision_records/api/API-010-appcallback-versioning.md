# API-010: Do not implement App Callback Versioning for HTTP

## Status
Accepted

## Context
There was a proposal to introducing versioning for HTTP App Callbacks. The goal of this review was to understand if a versioning was required and how it could handle situations post v1.0 of DAPR

## Decisions

- Introducing versioning to app callback APIs would require changes to the user applications which is not feasible
- There would be no way for DAPR runtime to find out the app callback version before hand

We decided not to introduce such a versioning scheme on the app callback APIs. Post v1.0, if required, the versioning could be implemented inside the payload but not on the API itself. A missing version in the payload could imply v1.0.