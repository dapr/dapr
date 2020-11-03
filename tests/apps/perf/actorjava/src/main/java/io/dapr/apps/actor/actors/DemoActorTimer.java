/*
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT License.
 */

package io.dapr.apps.actor.actors;

import io.dapr.actors.ActorType;

/**
 * Example of implementation of an Actor.
 */
@ActorType(name = "DemoActorTimer")
public interface DemoActorTimer {

  void registerDemoActorTimer();

  String say(String something);
}