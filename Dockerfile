# One image: refract + tectonic + synctex + git + ripgrep, non-root, data in /data.
# Build for one platform with `docker build .`, or both with `make docker`.

# --- frontend (platform-independent output, built natively) ---
FROM --platform=$BUILDPLATFORM node:22.23.4-alpine3.24 AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# --- Go binary (cross-compiled, no CGO) ---
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.24 AS go
ARG TARGETOS TARGETARCH
# Shown by `refract version`; the release workflow passes the tag.
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY internal internal
COPY web/embed.go web/
COPY --from=web /src/web/dist web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/refract ./cmd/refract

# --- synctex CLI from the official parser sources (Tectonic ships none) ---
FROM alpine:3.24.3 AS synctex
# Tag 2023.4(TeXLive); the commit hash pins the content.
ARG SYNCTEX_COMMIT=c3641da45b41cd61f9e625fe722d0a8d54cedaa9
RUN apk add --no-cache build-base git zlib-dev zlib-static
RUN git clone https://github.com/jlaurens/synctex.git /src \
 && git -C /src checkout --detach $SYNCTEX_COMMIT
WORKDIR /src
# Outside TeX Live, synctex_main.c expects <w2c/c-auto.h>; point it at the
# repo's own config header. (__SYNCTEX_WORK__ is the author's local setup and
# hardcodes a zlib path, so it is not used.)
RUN mkdir -p w2c /out \
 && echo '#include "synctex_parser_c-auto.h"' > w2c/c-auto.h \
 && cc -O2 -static -I. synctex_main.c synctex_parser.c synctex_parser_utils.c -lz -o /out/synctex

# --- Tectonic, pinned official static build, checksum verified ---
FROM --platform=$BUILDPLATFORM alpine:3.24.3 AS tectonic
ARG TARGETARCH
ARG TECTONIC_VERSION=0.17.0
RUN case "$TARGETARCH" in \
      amd64) triple=x86_64-unknown-linux-musl  sha=8533d07f9ccbd7a65824b9e0459041bca34af1eb33daba48f59215593753a3b7 ;; \
      arm64) triple=aarch64-unknown-linux-musl sha=b10954a95404f3ab2328d2fa59a5ebab8e657f893fab096f98be8db7c0c979b8 ;; \
      *) echo "unsupported arch $TARGETARCH" >&2; exit 1 ;; \
    esac \
 && wget -qO /tmp/t.tgz "https://github.com/tectonic-typesetting/tectonic/releases/download/tectonic%40${TECTONIC_VERSION}/tectonic-${TECTONIC_VERSION}-${triple}.tar.gz" \
 && echo "$sha  /tmp/t.tgz" | sha256sum -c - \
 && tar -xzf /tmp/t.tgz -C /usr/local/bin tectonic

# --- runtime ---
FROM alpine:3.24.3
RUN apk add --no-cache ca-certificates git ripgrep \
 && adduser -D -u 10001 refract \
 && mkdir /data && chown refract:refract /data
COPY --from=tectonic /usr/local/bin/tectonic /usr/local/bin/
COPY --from=synctex /out/synctex /usr/local/bin/
COPY --from=go /out/refract /usr/local/bin/
# The bundle is pinned for reproducible builds (the relay URL is a moving redirect).
ENV REFRACT_DATA_DIR=/data \
    TECTONIC_CACHE_DIR=/data/tectonic-cache \
    REFRACT_TECTONIC_BUNDLE=https://data1b.fullyjustified.net/tlextras-2022.0r0.tar
USER refract
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["refract"]
