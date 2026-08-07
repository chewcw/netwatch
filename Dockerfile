FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags="-s -w -X github.com/chewcw/netwatch/internal/cli.version=${VERSION}" \
    -o /out/netwatch ./cmd/netwatch

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/netwatch /netwatch
ENTRYPOINT ["/netwatch"]
