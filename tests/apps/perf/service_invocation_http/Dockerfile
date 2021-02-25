# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.
# ------------------------------------------------------------

FROM golang:1.15 as build_env

WORKDIR /app
COPY app.go .
RUN go get -d -v
RUN go build -o app .

FROM debian:buster-slim
WORKDIR /
COPY --from=build_env /app/app /
CMD ["/app"]
