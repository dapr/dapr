# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation and Dapr Contributors.
# Licensed under the MIT License.
# ------------------------------------------------------------

FROM golang:1.17 as build_env

WORKDIR /app
COPY app.go go.mod ./
RUN go get -d -v && go build -o app .

FROM debian:buster-slim
WORKDIR /
COPY --from=build_env /app/app /
CMD ["/app"]
