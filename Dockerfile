# Development image. All build/test/lint commands run inside this container.
FROM golang:1.27.4-bookworm

# staticcheck is installed as a prebuilt tool in the image; it is not a module dependency.
# Pin to a release that supports the Go version above.
ARG STATICCHECK_VERSION=2026.2.2
RUN GOBIN=/usr/local/bin GOFLAGS= GOPROXY=https://proxy.golang.org,direct \
    go install honnef.co/go/tools/cmd/staticcheck@${STATICCHECK_VERSION} \
 && rm -rf /root/go /root/.cache

# zip builds the Windows release archive (scripts/package.sh); tar, gzip and sha256sum
# are already in the image.
RUN apt-get update \
 && apt-get install -y --no-install-recommends zip \
 && rm -rf /var/lib/apt/lists/*

# Caches live in named volumes and must be writable by any UID (the container runs as the host user).
RUN mkdir -p /cache/go-build /cache/gomod /cache/home && chmod -R 1777 /cache

ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=local \
    GOPROXY=off \
    GOFLAGS=-mod=readonly \
    GOCACHE=/cache/go-build \
    GOMODCACHE=/cache/gomod \
    HOME=/cache/home \
    STATICCHECK_CACHE=/cache/go-build/staticcheck

WORKDIR /work
