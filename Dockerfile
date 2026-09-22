# Builder always runs on the native build platform and cross-compiles to the
# target arch (CGO is off, so Go cross-compiles trivially). This avoids QEMU
# emulation of an arm64 toolchain, which made the multi-arch build ~10x slower.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
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

FROM gcr.io/distroless/static-debian12@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2
COPY --from=build /out/tibber-pulse-bot /tibber-pulse-bot
USER 65532:65532
ENTRYPOINT ["/tibber-pulse-bot"]
