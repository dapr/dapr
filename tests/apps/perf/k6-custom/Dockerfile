# Build the k6 binary with the extension
FROM golang:1.24.6 AS builder

WORKDIR $GOPATH/src/go.k6.io/k6

RUN go install go.k6.io/xk6/cmd/xk6@latest
RUN xk6 build v0.56.0 \
    --output /k6 \
    --with github.com/grafana/xk6-output-prometheus-remote@latest \
    --with github.com/grafana/xk6-disruptor@v0.3.13

# Use the operator's base image and override the k6 binary
FROM grafana/k6:latest
COPY --from=builder /k6 /usr/bin/k6
