FROM golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb
RUN apk add --no-cache openssh bash && adduser -D -s /bin/bash tester
COPY sshd_config /etc/ssh/sshd_config
COPY start.sh /start.sh
RUN chmod 755 /start.sh
EXPOSE 2222
ENTRYPOINT ["/bin/sh", "/start.sh"]
