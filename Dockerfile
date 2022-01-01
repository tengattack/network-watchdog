FROM golang:1.15-alpine3.12

ARG goproxy

# Download packages from aliyun mirrors
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
  && apk --update add --no-cache ca-certificates tzdata git

COPY . /go/src/github.com/tengattack/network-watchdog
RUN cd /go/src/github.com/tengattack/network-watchdog \
  && GO111MODULE=on GOPROXY=$goproxy GOOS=linux CGO_ENABLED=0 go install -v

FROM alpine:3.12

# Download packages from aliyun mirrors
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
  && apk --update add --no-cache ca-certificates tzdata

COPY --from=0 /go/bin/network-watchdog /

WORKDIR /
USER nobody

ENTRYPOINT ["/network-watchdog"]
