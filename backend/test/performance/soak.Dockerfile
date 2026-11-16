FROM golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0 GOMAXPROCS=2
RUN go test -c -o /out/soak.test ./internal/application && go build -o /out/soak-vnc ./fixtures/vnc
FROM postgres:18-alpine@sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5
COPY --from=build /out/ /app/
USER 10001:10001
ENTRYPOINT ["/app/soak.test"]
