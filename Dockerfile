# syntax=docker/dockerfile:1

# Build on the build platform and cross-compile; Go needs no emulation.
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# VERSION is injected by the release workflow. Unset for a local build, which
# then reports 0.0.0-dev.
ARG VERSION
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags="-s -w ${VERSION:+-X 'github.com/fireball1725/printshop-mcp/internal/version.Version=${VERSION}'}" \
    -o /out/printshop-mcp ./cmd/printshop-mcp

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/printshop-mcp /app/printshop-mcp
# The image serves streamable HTTP; PRINTSHOP_MCP_TOKEN must be set at run time.
ENV PRINTSHOP_MCP_LISTEN=:8090
EXPOSE 8090
USER nonroot:nonroot
ENTRYPOINT ["/app/printshop-mcp"]
