# Actor Load Test

This includes actor test application and load test driver client.

### Build

```
make build
```

> Note: stateactor and testclient will be generated under ./dist

### Build Docker image

```
docker build -t [your registry]/actorload .
```

### Run test locally

1. Run StateActor Service

```
% dapr run --app-id stateactor --app-port 3000 -- ./dist/stateactor -p 3000

% ./stateactor --help
Usage of ./stateactor:
  -actors string
        Actor types array separated by comma. e.g. StateActor,SaveActor (default "StateActor")
  -p int
        StateActor service app port. (default 3000)
```

2. Start load test client

```
# Target QPS: 1000 qps, Number of test actors: 200, Duration: 30 mins
% dapr run --app-id testclient -- ./dist/testclient -qps 1000 -numactors 200 -t 30m

% ./testclient --help
Usage of ./testclient:
  -a string
        Actor Type (default "StateActor")
  -c int
        Number of parallel simultaneous connections. (default 10)
  -logcaller
        Logs filename and line number of callers to log (default true)
  -loglevel value
        loglevel, one of [Debug Verbose Info Warning Error Critical Fatal] (default Info)
  -logprefix string
        Prefix to log lines before logged messages (default "> ")
  -m string
        test actor method that will be called during test. e.g. nop, setActorState, getActorState (default "setActorState")
  -numactors int
        Number of randomly generated actors. (default 10)
  -qps float
        QPS per thread. (default 100)
  -s int
        The size of save state value. (default 1024)
  -t duration
        How long to run the test. (default 1m0s)
```
