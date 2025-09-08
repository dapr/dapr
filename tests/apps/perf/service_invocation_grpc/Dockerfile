#
# Copyright 2021 The Dapr Authors
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#     http://www.apache.org/licenses/LICENSE-2.0
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

FROM golang:1.24.6 as build_env

ENV CGO_ENABLED=0
WORKDIR /app
COPY go.mod go.sum app.go .
RUN go get -d -v
RUN go build -o app .

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build_env /app/app /
CMD ["/app"]
