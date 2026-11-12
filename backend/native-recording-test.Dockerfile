# Development-only recorder acceptance; no production listener is enabled.
FROM nt-native-rdp-worker:dev AS worker
FROM nt-native-rdp-sdk:3.32.2
RUN apk add --no-cache ffmpeg
COPY --from=worker /opt/freerdp /opt/freerdp
COPY --from=worker /opt/nt/bin /opt/nt/bin
COPY native/rdp/desktop_fixture.c /src/nt-rdp/desktop_fixture.c
RUN export PKG_CONFIG_PATH=/opt/freerdp/lib/pkgconfig && cc -std=c11 -Wall -Wextra -Werror -Wno-deprecated-declarations -O2 /src/nt-rdp/desktop_fixture.c -o /opt/nt/bin/nt-rdp-desktop-fixture $(pkg-config --cflags --libs freerdp-server3 freerdp3 winpr3) -Wl,--disable-new-dtags,-rpath,/opt/freerdp/lib
ENTRYPOINT []
