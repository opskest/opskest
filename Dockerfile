# syntax=docker/dockerfile:1.7
FROM node:24-slim AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build -- --outDir /out --emptyOutDir

FROM golang:1.27.3-bookworm AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /out ./internal/webui/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /opskest ./cmd/opskest

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /opskest /opskest
USER nonroot:nonroot
ENV DATA_DIR=/data BACKUP_DIR=/backups LISTEN_ADDR=:8080
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["/opskest", "healthcheck"]
ENTRYPOINT ["/opskest"]
CMD ["serve"]
