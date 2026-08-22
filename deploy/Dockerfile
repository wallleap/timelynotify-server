FROM golang:1-alpine AS builder

COPY . /go/src/github.com/wallleap/timelynotify-server

WORKDIR /go/src/github.com/wallleap/timelynotify-server

# Use China-accessible Go module proxy by default (proxy.golang.org is often
# unreachable from CN networks, causing `go: downloading ... i/o timeout`).
# Override with `--build-arg GOPROXY=...`, e.g. GOPROXY=direct for a cached/local vm.
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}

# Use a China-accessible Alpine mirror for apk (dl-cdn.alpinelinux.org is
# slow/unreachable from CN networks). Override with --build-arg ALPINE_MIRROR=...
ARG ALPINE_MIRROR=https://mirrors.aliyun.com
RUN sed -i "s|https\?://[^/]*/alpine|${ALPINE_MIRROR%/}/alpine|" /etc/apk/repositories

RUN set -ex \
    # && apk add git gcc libc-dev \
    && apk add git libc-dev \
    && BUILD_VERSION=$(git describe --tags) \
    && BUILD_DATE=$(date "+%F %T") \
    && COMMIT_SHA1=$(git rev-parse HEAD) \
    && go install -trimpath -ldflags \
            "-X 'main.version=${BUILD_VERSION}' \
             -X 'main.buildDate=${BUILD_DATE}' \
             -X 'main.commitID=${COMMIT_SHA1}' \
             -w -s"

FROM alpine

LABEL maintainer="mritd <mritd@linux.com>"

# set up nsswitch.conf for Go's "netgo" implementation
# - https://github.com/golang/go/blob/go1.9.1/src/net/conf.go#L194-L275
# - docker run --rm debian:stretch grep '^hosts:' /etc/nsswitch.conf
RUN echo 'hosts: files dns' > /etc/nsswitch.conf

# default timezon
# override it with `--build-arg TIMEZONE=xxxx`
ARG TIMEZONE=Asia/Shanghai

ENV TZ=${TIMEZONE}

# Same Alpine mirror as the builder stage (default Aliyun, CN-accessible).
ARG ALPINE_MIRROR=https://mirrors.aliyun.com
RUN sed -i "s|https\?://[^/]*/alpine|${ALPINE_MIRROR%/}/alpine|" /etc/apk/repositories

RUN set -ex \
    && apk upgrade \
    && apk add --no-cache bash ca-certificates tzdata \
    && ln -sf /usr/share/zoneinfo/${TZ} /etc/localtime \
    && echo ${TZ} > /etc/timezone \
    && addgroup -S -g 1000 app \
    && adduser -S -u 1000 -h /home/app -G app app

COPY --from=builder /go/bin/timelynotify-server /usr/local/bin/timelynotify-server
COPY --from=builder /go/src/github.com/wallleap/timelynotify-server/deploy/entrypoint.sh /entrypoint.sh

# Default data dir (overridable via BARK_SERVER_DATA_DIR / --data). Make it
# writable by the non-root app user and switch to least privilege.
ENV BARK_SERVER_DATA_DIR=/data
RUN mkdir -p /data && chown -R app:app /data && chmod 755 /usr/local/bin/timelynotify-server /entrypoint.sh

USER app

VOLUME /data

EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]

CMD ["timelynotify-server"]
