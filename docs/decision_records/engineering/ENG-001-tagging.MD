# ENG-001: Image Tagging

## Status
Accepted

## Context
As we embraced using Docker repositories to store our images, and keeping in mind we support multiple repositories along with versioning of images and different architectures,
We needed a way to construct an accepted and constant way of naming our Docker images.

## Decisions

* An image will conform to the following format: \<namespace>/\<repository>:\<tag>
* A valid tag conforms to the following format: \<version>-\<architecture>, or just \<version>, then arch is assumed Linux
  
## Consequences

This keeps us constant with widely accepted naming conventions and sets clear guidelines for naming of future images.

## Examples

Dapr Runtime, latest Linux:

actionscore.azurecr.io/dapr:latest

Dapr Runtime, v0.1.0-alpha for ARM:

actionscore.azurecr.io/dapr:v0.1.0-alpha-arm
