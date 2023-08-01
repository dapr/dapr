#!/usr/bin/env bash

set -ue

SECONDS_FOR_PROCESS_TO_RUN=${SECONDS_FOR_PROCESS_TO_RUN:-5}
GOOS=${GOOS:-$(go env GOOS)}
GOARCH=${GOARCH:-$(go env GOARCH)}
LIMIT_DELTA_BINARY_SIZE=${LIMIT_DELTA_BINARY_SIZE:-1024} # KB (1 MB)
LIMIT_DELTA_VIRT_MEM=${LIMIT_DELTA_VIRT_MEM:-3200}  # KB
LIMIT_DELTA_GO_ROUTINES=${LIMIT_DELTA_GO_ROUTINES:-0} # Does not accept any extra Go routines

./dist/${GOOS}_${GOARCH}/release/daprd --app-id target &
pid=$!
sleep $SECONDS_FOR_PROCESS_TO_RUN
BINARY_SIZE=$(( `wc -c ./dist/${GOOS}_${GOARCH}/release/daprd | awk '{print $1}'` / 1024 ))
RESIDENT_MEM=`ps -o rss= $pid`
VIRT_MEM=`ps -o vsz= $pid`
GO_ROUTINES=`curl -s http://localhost:9090/metrics | grep go_goroutines | grep -v \# | cut -d\  -f2`
kill -TERM $pid
wait $pid

BASELINE_BINARY=`readlink -f ~/.dapr/bin/daprd`
$BASELINE_BINARY --app-id baseline &
baseline_pid=$!
sleep $SECONDS_FOR_PROCESS_TO_RUN
BASELINE_BINARY_SIZE=$(( `wc -c $BASELINE_BINARY | awk '{print $1}'` / 1024 ))
BASELINE_RESIDENT_MEM=`ps -o rss= $baseline_pid`
BASELINE_VIRT_MEM=`ps -o vsz= $baseline_pid`
BASELINE_GO_ROUTINES=`curl -s http://localhost:9090/metrics | grep go_goroutines | grep -v \# | cut -d\  -f2`
kill -TERM $baseline_pid
wait $baseline_pid

DELTA_BINARY_SIZE=$(( $BINARY_SIZE - $BASELINE_BINARY_SIZE ))
DELTA_RESIDENT_MEM=$(( $RESIDENT_MEM - $BASELINE_RESIDENT_MEM ))
DELTA_VIRT_MEM=$(( $VIRT_MEM - $BASELINE_VIRT_MEM ))
DELTA_GO_ROUTINES=$(( $GO_ROUTINES - $BASELINE_GO_ROUTINES ))

echo "Binary size: $BINARY_SIZE KB compared to baseline of $BASELINE_BINARY_SIZE KB ($DELTA_BINARY_SIZE KB)"
echo "Resident memory: $RESIDENT_MEM KB compared to baseline of $BASELINE_RESIDENT_MEM KB ($DELTA_RESIDENT_MEM KB)"
echo "Virtual memory: $VIRT_MEM KB compared to baseline of $BASELINE_VIRT_MEM KB ($DELTA_VIRT_MEM KB)"
echo "Number of Go routines: $GO_ROUTINES compared to baseline of $BASELINE_GO_ROUTINES ($DELTA_GO_ROUTINES)"

EXIT_CODE=0

if [[ $DELTA_BINARY_SIZE -gt $LIMIT_DELTA_BINARY_SIZE ]]; then
   echo "New version's binary size is too large: $DELTA_BINARY_SIZE KB"
   EXIT_CODE=1
fi

if [[ $DELTA_VIRT_MEM -gt $LIMIT_DELTA_VIRT_MEM ]]; then
   echo "New version is consuming too much virtual memory: $DELTA_VIRT_MEM KB"
   EXIT_CODE=1
fi

if [[ $DELTA_GO_ROUTINES -gt $LIMIT_DELTA_GO_ROUTINES ]]; then
   echo "New version is spawning an additional $DELTA_GO_ROUTINES Go routines"
   EXIT_CODE=1
fi

exit $EXIT_CODE
