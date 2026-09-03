# Builder always runs on the native build platform and cross-compiles to the
# target arch (CGO is off, so Go cross-compiles trivially). This avoids QEMU
# emulation of an arm64 toolchain, which made the multi-arch build ~10x slower.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:cf6fca6641884b8433441b2b0652976f975e1d0fdd26d177eaaf8596087f3125 AS build
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
