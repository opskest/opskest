# syntax=docker/dockerfile:1.7
# The web and Go stages run on the build machine and cross-compile, so one
# build produces both amd64 and arm64 images without emulation.
FROM --platform=$BUILDPLATFORM node:24-slim AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build -- --outDir /out --emptyOutDir

FROM --platform=$BUILDPLATFORM golang:1.27.3-bookworm AS build
ARG VERSION=dev
# Official images set these so installs check the project's signed releases
# for updates. A local build leaves them empty and checks nothing.
ARG UPDATE_PUBLIC_KEY=
ARG UPDATE_FEED_URL=
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /out ./internal/webui/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.updatePublicKey=${UPDATE_PUBLIC_KEY} -X main.updateFeedURL=${UPDATE_FEED_URL}" \
    -o /opskest ./cmd/opskest

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /opskest /opskest
USER nonroot:nonroot
ENV DATA_DIR=/data BACKUP_DIR=/backups LISTEN_ADDR=:8080
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["/opskest", "healthcheck"]
ENTRYPOINT ["/opskest"]
CMD ["serve"]
