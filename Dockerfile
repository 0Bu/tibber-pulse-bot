FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/tibber-pulse-bot ./cmd/tibber-pulse-bot

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/tibber-pulse-bot /tibber-pulse-bot
USER 65532:65532
ENTRYPOINT ["/tibber-pulse-bot"]
