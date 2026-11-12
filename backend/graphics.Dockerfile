FROM golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/nt-rdp-storage ./cmd/nt-rdp-storage
FROM guacamole/guacd:1.6.0@sha256:8974eaa9ba32f713daf311e7cc8cd7e4cdfba1edea39eed75524e78ef4b08f4f
USER root
RUN mkdir -p /var/lib/next-terminal/storage /mnt/nt-rdp && chown 10001:10001 /var/lib/next-terminal/storage
COPY --from=build /out/nt-rdp-storage /usr/local/bin/nt-rdp-storage
ENTRYPOINT ["/usr/local/bin/nt-rdp-storage"]
