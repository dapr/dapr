/*
 * Copyright (c) Microsoft Corporation and Dapr Contributors.
 * Licensed under the MIT License.
 */

package io.dapr.apps.actor.actors;

import java.io.IOException;

import io.dapr.actors.ActorType;
import io.dapr.apps.actor.Car;

/**
 * Example of implementation of an Actor.
 */
@ActorType(name = "JavaCarActor")
public interface CarActor {

  int IncrementAndGetAsync(int delta);

  String CarToJSONAsync(Car car) throws IOException;

  Car CarFromJSONAsync(String content) throws IOException;
}