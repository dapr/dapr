# Build the k6 binary with the extension
FROM golang:1.21 as builder

RUN go install go.k6.io/xk6/cmd/xk6@latest
RUN xk6 build --output /k6 --with github.com/grafana/xk6-output-prometheus-remote@latest --with github.com/grafana/xk6-disruptor@350f53204c65040e71757f98a330665a8f189f91

# Use the operator's base image and override the k6 binary
FROM loadimpact/k6:latest
COPY --from=builder /k6 /usr/bin/k6
