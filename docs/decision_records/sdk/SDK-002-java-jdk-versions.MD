# SDK-002: Java JDK Versions

## Status
Accepted

## Context
Dapr offers a Java SDK. Java 11 is the latest LTS version. Java 8 is the previous LTS version but still the mainly used version by the Java community in 2019. What should be the minimum Java version supported by Dapr's Java SDK?

See https://github.com/dapr/java-sdk/issues/17

## Decisions

* Java 8 should be the minimum version supported for Dapr's Java SDK.
* Java 11 should be used in samples and user documentation to encourage adoption.
* Java 8's commercial support ends in 2022. Dapr's Java SDK shoud migrate to Java 11 prior to that. The timeline still not decided.
  
## Consequences

* Customers running with Java 7 or below cannot use Dapr's Java SDK.
* Customers running with Java 8 are still supported, even through Java 11 is the recommended version.
* Modern language features will not be available in Dapr's Java SDK code.
* Modern JVM features can still be used since the Java 11 JVM can run Java 8 bytecode.

