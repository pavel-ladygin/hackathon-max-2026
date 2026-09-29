FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN mkdir -p /out \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/ \
    ./cmd/server ./cmd/migrate ./cmd/seed ./cmd/event-sync ./cmd/import-events ./cmd/daily-notifications

FROM build AS maintenance-build
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/backfill-timepad-posters ./cmd/backfill-timepad-posters

FROM alpine:3.22 AS runtime
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/server /out/migrate /out/seed /out/event-sync /out/import-events /out/daily-notifications ./
USER 65532:65532
EXPOSE 8080
CMD ["/app/server"]

FROM runtime AS maintenance
COPY --from=maintenance-build /out/backfill-timepad-posters ./

# В стандартный образ не входит утилита восстановления постеров.
FROM runtime AS product
