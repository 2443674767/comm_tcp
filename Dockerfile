FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
ARG GOPROXY=https://proxy.golang.org,direct
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /lanchat ./cmd/lanchat

FROM scratch
COPY --from=build /lanchat /lanchat
ENV DATA_DIR=/data TLS_CERT=/certs/server.crt TLS_KEY=/certs/server.key TLS_CA=/certs/ca.crt
EXPOSE 18086/tcp 18087/udp 18085/tcp
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/lanchat", "healthcheck"]
ENTRYPOINT ["/lanchat"]
