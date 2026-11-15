FROM puppeteer/installer-test:local AS cli
FROM python:3.11-slim
ENV container=docker
RUN apt-get update && apt-get install -y --no-install-recommends systemd systemd-sysv curl ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=cli /lib/ld-musl-x86_64.so.1 /lib/ld-musl-x86_64.so.1
COPY --from=cli /usr/bin/docker /usr/bin/docker
COPY --from=cli /usr/libexec/docker/cli-plugins/docker-compose /usr/libexec/docker/cli-plugins/docker-compose
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
