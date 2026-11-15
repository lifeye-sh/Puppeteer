FROM golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Bound compiler concurrency on shared development hosts.
ENV GOMAXPROCS=2
RUN CGO_ENABLED=0 go build -trimpath -o /out/server ./cmd/server && \
    CGO_ENABLED=0 go build -trimpath -o /out/nt ./cmd/nt && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /out/nt-agent-linux-amd64 ./cmd/nt-agent && \
    CGO_ENABLED=0 go build -trimpath -o /out/nt-agent-release ./cmd/nt-agent-release && \
    CGO_ENABLED=0 go build -trimpath -o /out/proxy-fixture ./fixtures/proxy && \
    CGO_ENABLED=0 go build -trimpath -o /out/release-fixture ./fixtures/releases && \
    CGO_ENABLED=0 go build -trimpath -o /out/telnet-fixture ./fixtures/telnet && \
    CGO_ENABLED=0 go build -trimpath -o /out/vnc-fixture ./fixtures/vnc
FROM postgres:18-alpine@sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/ /app/
RUN mkdir -p /var/lib/puppeteer/mp4 /var/lib/puppeteer/backups /var/lib/puppeteer/restore /var/lib/puppeteer/storage /var/lib/puppeteer/runtime && chown -R 10001:10001 /var/lib/puppeteer
USER 10001:10001
EXPOSE 8089
ENTRYPOINT ["/app/server"]
