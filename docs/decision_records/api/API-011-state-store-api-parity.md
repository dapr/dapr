# API-001: State Store APIs Parity

## Status
Accepted

## Context
We reviewed parity of state store APIs .

## Decisions

* GetState APIs continue to have Single Key Get and Bulk Get APIs behaviour as current 0.10.0 version.
* SaveState API will continue to have one SaveState API endpoint. If user wants to save single key, same save state API will be used
  for passing single item in the bulk set.
  
  Potential issues arises if following new single key save state API is introduced:
     
     `Post : state/{storeName}/{key}`  

  This will conflict with 
    - State Transaction API, if the key is "transaction"
    - GetBulkState API, if the key is "bulk"
  
  So the decision is to continue the Save State API behaviour as current 0.10.0 version.

* Bulk Delete API might come in future versions based on the scenarios.

## Consequences

No changes needed to bring the parity among state store APIs. APIs continue to remain same as current 0.10.0 version.