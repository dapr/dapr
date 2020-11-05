FROM golang:1.15 as build_env

WORKDIR /app
COPY app.go .
RUN go get -d -v
RUN go build -o tester .

FROM golang:1.15 as fortio_build_env

WORKDIR /fortio
RUN git clone https://github.com/artursouza/fortio.git #redo
RUN cd fortio && go build

FROM debian:buster-slim
RUN apt update
RUN apt install wget -y
WORKDIR /
COPY --from=build_env /app/tester /
COPY --from=fortio_build_env /fortio/fortio/fortio /usr/local/bin
CMD ["/tester"]
