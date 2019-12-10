# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
# ------------------------------------------------------------

FROM golang:1.13

WORKDIR /app
COPY app.go .
RUN go get -d -v
RUN go build -o app .

CMD ["./app"]