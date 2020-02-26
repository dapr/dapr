FROM --platform=$TARGETPLATFORM alpine:latest as alpine
ARG TARGETPLATFORM
RUN apk add -U --no-cache ca-certificates
RUN mkdir -p /linux/amd64
ADD ./linux_amd64/release /linux/amd64
RUN mkdir -p /linux/arm/v7
ADD ./linux_arm/release /linux/arm/v7
RUN mkdir /release
# Copy only target platform to root
RUN cp /$TARGETPLATFORM/* /release

FROM scratch
ENTRYPOINT []
WORKDIR /
COPY --from=alpine /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=alpine /release /
