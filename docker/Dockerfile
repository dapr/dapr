FROM alpine:latest as alpine
ARG TARGETPLATFORM
RUN apk add -U --no-cache ca-certificates
# current directory must be ./dist
ADD . /dist
RUN if [ $TARGETPLATFORM = linux/amd64 ]; then mkdir -p /dist/linux/amd64 && mv /dist/linux_amd64/release /dist/linux/amd64/; fi
RUN if [ $TARGETPLATFORM = linux/arm/v7 ]; then mkdir -p /dist/linux/arm/v7 && mv /dist/linux_arm/release /dist/linux/arm/v7/; fi

FROM scratch
ARG TARGETPLATFORM
ENTRYPOINT []
WORKDIR /
COPY --from=alpine /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=alpine /dist/$TARGETPLATFORM/release /
