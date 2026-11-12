FROM golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
RUN CGO_ENABLED=0 go install github.com/letsencrypt/pebble/v2/cmd/pebble@v2.10.1
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/fixture-services ./fixtures/services
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/fixture-services /app/fixture-services
COPY --from=build /go/bin/pebble /app/pebble
USER 10001:10001
ENTRYPOINT ["/app/fixture-services"]
