# SDK-001: SDK Releases

## Status
Accepted

## Context
Dapr exposes apis for building blocks which can be invoked over http/gRPC by the user code. Making raw http/gRPC calls from user code works but it doesn't provide a good strongly typed exeprience for developers.

## Decisions

* Dapr will provide language specific SDKs for developers for C#, Java, Javascript, Python, Go, Rust, C++. 
  - For initial release, SDKs would be auto-generated from the Dapr proto specifications using gRPC tools.
  - In future release, team will work on creating and releasing strongly typed SDKs for the languages, this can be a wrapper on top of auto-generated gRPC SDKs or  handcrafted. e.g. C# SDK shipped for state management APIs with the 0.1.0 release.
* For Actors, language specific SDKs will be shipped by the team as writing Actor specific code can be greatly simplified by users by providing these SDKs. e.g. C# Actor SDK shipped with the 0.1.0 release.
  
## Consequences

Auto-generation of gRPC client side code form Dapr proto files allows us to provide SDKs for major languages with 0.1.0 rwlease and sets up on the correct path to generate more user friendly SDKs by wrapping the auto-generated gRPC ones.

