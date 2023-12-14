# syntax=docker/dockerfile:1
FROM golang:alpine AS builder
WORKDIR $GOPATH/src/app
ADD . ./
ENV GO111MODULE on
ENV GOPROXY="https://goproxy.io"
WORKDIR $GOPATH/src/app/cmd/gmqttd
RUN go build

FROM alpine:3.12
WORKDIR /gmqttd
# RUN apk update && apk add --no-cache tzdata
COPY --from=builder /go/src/app/cmd/gmqttd .
EXPOSE 1883 8883 8082 8083 8084
RUN chmod +x gmqttd
RUN pwd
RUN ls -lrt
ENTRYPOINT ["./gmqttd", "start", "-c", "/gmqttd/default_config.yml"]
