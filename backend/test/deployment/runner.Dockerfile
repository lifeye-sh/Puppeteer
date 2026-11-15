# Isolated deployment acceptance runner; never used as the application image.
FROM postgres:18-alpine@sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5
RUN apk add --no-cache bash curl python3 openssl docker-cli docker-cli-compose
ENTRYPOINT ["sleep"]
CMD ["infinity"]
