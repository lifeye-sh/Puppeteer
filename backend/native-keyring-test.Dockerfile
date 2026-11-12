# Isolated acceptance fixture only: no host keyring or session bus is mounted.
FROM nt-native-rdp-worker:dev
USER root
RUN apk add --no-cache dbus gnome-keyring libsecret && \
    adduser -D -u 10001 keyring-test && \
    mkdir -p /run/user/10001 && chown keyring-test:keyring-test /run/user/10001 && chmod 700 /run/user/10001
ENV HOME=/home/keyring-test XDG_RUNTIME_DIR=/run/user/10001
USER keyring-test
ENTRYPOINT []
