# SDK-001: SDK Releases

## Status
Accepted

## Context
Dapr exposes APIs for building blocks which can be invoked over http/gRPC by the user code. Making raw http/gRPC calls from user code works but it doesn't provide a good strongly typed experience for developers.

## Decisions

* Dapr provides language specific SDKs for developers for C#, Java, Javascript, Python, Go, Rust, C++. There may be others in the future
  - For the current release, the SDKs are auto-generated from the Dapr proto specifications using gRPC tools.
  - In future releases, we will work on creating and releasing strongly typed SDKs for the languages, which are wrappers on top of the auto-generated gRPC SDKs (e.g. C# SDK shipped for state management APIs with the 0.1.0 release.) This is the preferred approach. Creating purely handcrafted SDKs is discouraged.
* For Actors, language specific SDKs are written as Actor specific handcrafted code is preferred since this greatly simplifies the user experience. e.g. The C# Actor SDK shipped with the 0.1.0 release.
  
## Consequences

Auto-generation of gRPC client side code from Dapr proto files allows Dapr to provide SDKs for the major languages with the 0.1.0 release and set us on the correct path to generate more user friendly SDKs by wrapping the auto-generated gRPC ones. There will be no auto-generated code for actor SDKs, which are also handcrafted to focus on API usability.

