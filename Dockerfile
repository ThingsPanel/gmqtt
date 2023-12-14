FROM golang:alpine AS builder
# RUN apk update && apk add --no-cache make git
ADD . /go/src/github.com/ThingsPanel/gmqtt
WORKDIR /go/src/github.com/ThingsPanel/gmqtt/cmd/gmqttd
ENV GO111MODULE on
ENV GOPROXY="https://goproxy.io"
RUN go build
# RUN make binary

FROM alpine:3.12
WORKDIR /gmqttd
# RUN apk update && apk add --no-cache tzdata
COPY --from=builder /go/src/github.com/ThingsPanel/gmqtt/cmd/gmqttd .
# RUN mkdir /etc/gmqtt
# ENV PATH=$PATH:/gmqttd
EXPOSE 1883 8883 8082 8083 8084
RUN chmod +x gmqttd
# ENTRYPOINT ["gmqttd","start"]
RUN pwd
RUN ls -lrt
ENTRYPOINT ["./gmqttd", "start", "-c", "/gmqttd/gmqttd.yml"]
