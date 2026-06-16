# Builder always runs on the native build platform and cross-compiles to the
# target arch (CGO is off, so Go cross-compiles trivially). This avoids QEMU
# emulation of an arm64 toolchain, which made the multi-arch build ~10x slower.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine@sha256:f1ddd9fe14fffc091dd98cb4bfa999f32c5fc77d2f2305ea9f0e2595c5437c14 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
# TARGETOS/TARGETARCH are provided automatically by buildx per --platform.
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/tibber-pulse-bot ./cmd/tibber-pulse-bot

FROM gcr.io/distroless/static-debian12@sha256:9c346e4be81b5ca7ff31a0d89eaeade58b0f95cfd3baed1f36083ddb47ca3160
COPY --from=build /out/tibber-pulse-bot /tibber-pulse-bot
USER 65532:65532
ENTRYPOINT ["/tibber-pulse-bot"]
