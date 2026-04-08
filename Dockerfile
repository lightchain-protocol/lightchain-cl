FROM golang:1.25-alpine AS builder

RUN apk add --no-cache gcc g++ musl-dev linux-headers git

COPY go.mod go.sum /prysm/
COPY third_party /prysm/third_party
WORKDIR /prysm
RUN go mod download

COPY . /prysm

RUN go build -o /out/beacon-chain ./cmd/beacon-chain
RUN go build -o /out/validator ./cmd/validator
RUN go build -o /out/prysmctl ./cmd/prysmctl

FROM alpine:latest

RUN apk add --no-cache ca-certificates bash

COPY --from=builder /out/beacon-chain /usr/local/bin/
COPY --from=builder /out/validator /usr/local/bin/
COPY --from=builder /out/prysmctl /usr/local/bin/
