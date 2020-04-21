FROM golang:1.14 as build_env

WORKDIR /app
COPY app.go .
RUN go get -d -v
RUN go build -o tester .

FROM debian:buster-slim
RUN apt update
RUN apt install wget -y
WORKDIR /
COPY --from=build_env /app/tester /
RUN wget https://github.com/fortio/fortio/releases/download/v1.3.1/fortio-linux_x64-1.3.1.tgz
RUN tar -xvf fortio-linux_x64-1.3.1.tgz
RUN cp /usr/bin/fortio /usr/local/bin
CMD ["/tester"]
