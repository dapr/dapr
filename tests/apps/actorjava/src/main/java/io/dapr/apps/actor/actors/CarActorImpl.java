/*
 * Copyright (c) Microsoft Corporation and Dapr Contributors.
 * Licensed under the MIT License.
 */

package io.dapr.apps.actor.actors;

import java.io.IOException;
import java.util.concurrent.atomic.AtomicInteger;

import com.fasterxml.jackson.databind.ObjectMapper;

import io.dapr.actors.ActorId;
import io.dapr.actors.runtime.AbstractActor;
import io.dapr.actors.runtime.ActorRuntimeContext;
import io.dapr.apps.actor.Car;

/*
 * Copyright (c) Microsoft Corporation and Dapr Contributors.
 * Licensed under the MIT License.
 */
public class CarActorImpl extends AbstractActor implements CarActor {

    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();

    private final AtomicInteger counter = new AtomicInteger();

    /**
     * This is the constructor of an actor implementation.
     *
     * @param runtimeContext The runtime context object which contains objects such
     *                       as the state provider.
     * @param id             The id of this actor.
     */
    public CarActorImpl(ActorRuntimeContext<?> runtimeContext, ActorId id) {
        super(runtimeContext, id);
    }

    @Override
    public int IncrementAndGetAsync(int delta) {
        return this.counter.addAndGet(delta);
    }

    @Override
    public String CarToJSONAsync(Car car) throws IOException {
        if (car == null) {
            return "";
        }
        return OBJECT_MAPPER.writeValueAsString(car);
    }

    @Override
    public Car CarFromJSONAsync(String content) throws IOException {
        return OBJECT_MAPPER.readValue(content, Car.class);
    }
}
