# ------------------------------------------------------------
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
# ------------------------------------------------------------

FROM golang:1.14

WORKDIR /app
COPY . .
RUN go build -o app .

CMD ["./app"]
