# syntax=docker/dockerfile:1.7

# Base images pinned to minor+OS version for reproducibility.
# Bump these together when upgrading Go or the Alpine runtime base.
ARG GO_IMAGE=golang:1.25-alpine3.22
ARG RUNTIME_IMAGE=alpine:3.22

# ---------- Builder stage ----------
FROM ${GO_IMAGE} AS builder

# BLST (used by Prysm for BLS signatures) requires CGO + a C toolchain.
RUN apk add --no-cache gcc g++ musl-dev linux-headers git

WORKDIR /prysm

# Copy just the dependency manifests first so `go mod download` is cached
# across source changes. third_party/ must be present because go.mod has
# replace directives that point into it.
COPY go.mod go.sum ./
COPY third_party ./third_party
RUN go mod download

# Now copy the rest of the source.
COPY . .

ENV CGO_ENABLED=1 \
    GOFLAGS=-mod=readonly

# -trimpath removes absolute paths for reproducible builds; -s -w strips
# debug info to shrink the resulting binaries.
RUN go build -trimpath -ldflags="-s -w" -o /out/beacon-chain ./cmd/beacon-chain \
 && go build -trimpath -ldflags="-s -w" -o /out/validator    ./cmd/validator \
 && go build -trimpath -ldflags="-s -w" -o /out/prysmctl     ./cmd/prysmctl

# ---------- Runtime stage ----------
FROM ${RUNTIME_IMAGE}

# OCI labels for provenance. Override at build time with --build-arg.
ARG VCS_REF=unknown
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.title="lightchain-prysm" \
      org.opencontainers.image.description="LightChain Prysm consensus client (devnet build)" \
      org.opencontainers.image.source="https://github.com/LimeChain/lightchain-cl" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.licenses="GPL-3.0-or-later"

RUN apk add --no-cache ca-certificates bash tini \
 && addgroup -S prysm \
 && adduser  -S -G prysm -h /home/prysm prysm

COPY --from=builder /out/beacon-chain /usr/local/bin/beacon-chain
COPY --from=builder /out/validator    /usr/local/bin/validator
COPY --from=builder /out/prysmctl     /usr/local/bin/prysmctl

# Pre-create directories that docker-compose mounts as named volumes or that
# Prysm writes to at runtime. Without this, Docker creates the mount points
# as root:root and the non-root prysm user gets "permission denied".
RUN mkdir -p /beacondata /data && chown prysm:prysm /beacondata /data

USER prysm
WORKDIR /home/prysm

# tini reaps zombie processes and forwards signals cleanly to the beacon
# node — important when `docker stop` needs the process to shut down fast.
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["beacon-chain", "--help"]
