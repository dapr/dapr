/*
 * Copyright (c) Microsoft Corporation and Dapr Contributors.
 * Licensed under the MIT License.
 */

package io.dapr.apps.actor.actors;

import static org.apache.commons.lang3.math.NumberUtils.createDouble;
import static org.apache.commons.lang3.math.NumberUtils.isParsable;

import java.text.DateFormat;
import java.text.SimpleDateFormat;
import java.time.Duration;
import java.util.Calendar;
import java.util.Random;
import java.util.TimeZone;

import io.dapr.actors.ActorId;
import io.dapr.actors.runtime.AbstractActor;
import io.dapr.actors.runtime.ActorRuntimeContext;
import lombok.extern.slf4j.Slf4j;

/**ß
 * Implementation of the DemoActor for the server side.
 */
@Slf4j
public class DemoActorTimerImpl extends AbstractActor implements DemoActorTimer {

    private static final Random RANDOM = new Random(37);

    /**
     * Format to output date and time.
     */
    private final DateFormat dateFormat = new SimpleDateFormat("yyyy-MM-dd HH:mm:ss.SSS");

    /**
     * Ratio to which messages are logged, default is 1.0 (100%)
     */
    private final double logRatio;

    /**
     * This is the constructor of an actor implementation.
     *
     * @param runtimeContext The runtime context object which contains objects such as the state provider.
     * @param id             The id of this actor.
     */
    public DemoActorTimerImpl(ActorRuntimeContext runtimeContext, ActorId id) {
        super(runtimeContext, id);

        String logRatioString = System.getenv("LOG_RATIO");
        double logRatioTemp = isParsable(logRatioString) ? createDouble(logRatioString) : 1;
        logRatioTemp = logRatioTemp < 0 ? 0 : logRatioTemp;
        logRatioTemp = logRatioTemp > 1 ? 1 : logRatioTemp;
        this.logRatio = logRatioTemp;
    }

    @Override
    public void registerDemoActorTimer() {
        super.registerActorTimer(
                "sayTimer",
                "say",
                "Hello World!",
                Duration.ofSeconds(10),
                Duration.ofSeconds(10))
                .subscribe(
                        avoid -> log.info("timer registered successfully"),
                        error -> log.warn(error.getMessage()),
                        () -> log.info("registerTimer completed"));
    }

    /**
     * Prints a message and appends the timestamp.
     *
     * @param something Something to be said.
     * @return Timestamp.
     */
    @Override
    public String say(String something) {
        Calendar utcNow = Calendar.getInstance(TimeZone.getTimeZone("GMT"));
        String utcNowAsString = dateFormat.format(utcNow.getTime());
        String timestampedMessage = something == null ? "" : something + " @ " + utcNowAsString;

        // Handles the request by printing message.
        if (RANDOM.nextDouble() < logRatio) {
            log.info("Server say method for actor " + super.getId() + ": " + timestampedMessage);
        }

        // Now respond with current timestamp.
        return utcNowAsString;
    }

    @Override
    public void noOp() {
        // No-op to test performance without app logic impacting numbers.
    }
}
