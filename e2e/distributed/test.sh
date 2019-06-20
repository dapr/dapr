#!/bin/bash
SCENARIO=""
PROFILE=""
MESSAGE_SIZE=""
MESSAGE_COUNT=0
if [ "$1" != "" ]; then
    SCENARIO=$1
    shift
fi

if [ "$SCENARIO" = "" ]; then
    echo "Usage: test <scenario name>"
    exit 1
fi

if [ ! -d "$SCENARIO" ]; then
    echo "test scenario has to be a folder"
    exit 1
fi

if [ ! -f "$SCENARIO/scenario.config" ]; then
    echo "config file is not found"
    exit 1
fi

. "$SCENARIO/scenario.config"

INDEX=1
VAR=vm${INDEX}
TARGET=vm${INDEX}_target
PORT=vm${INDEX}_port

until [ "${!VAR}" == "" ]; do
    ssh $uname@${!VAR} "$GOPATH/src/github.com/actionscore/actions/e2e/distributed/$SCENARIO/$VAR/launch.sh $SCENARIO $VAR $msg_count $msg_size ${!TARGET} ${!PORT}" &
    PIDS+=($!)
    let INDEX+=1
    VAR=vm${INDEX}
    TARGET=vm${INDEX}_target
    PORT=vm${INDEX}_port
done
for pid in ${PIDS[@]}; do
    echo "pid=${pid}"
    wait ${pid}
    STATUS+=($?)
done
for st in ${STATUS[@]}; do
    echo st
done
