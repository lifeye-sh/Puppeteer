# Isolated authentication probe and recorded view-only desktop profiles.
FROM nt-native-rdp-sdk:3.32.2
ARG NT_EXTRA_CFLAGS=""
COPY native/rdp/gdi-forward.patch /src/nt-rdp/gdi-forward.patch
COPY native/rdp/rdpdr-policy.patch /src/nt-rdp/rdpdr-policy.patch
RUN cd /src/FreeRDP-3.32.2 && patch -p1 < /src/nt-rdp/gdi-forward.patch \
 && patch -p1 < /src/nt-rdp/rdpdr-policy.patch \
 && cmake --build /src/build --target freerdp-server-proxy --parallel 2 \
 && cmake --install /src/build/server/proxy
COPY native/rdp/worker.c native/rdp/capture.c native/rdp/capture.h native/rdp/probe_client.c /src/nt-rdp/
COPY native/rdp/channel_policy.h native/rdp/channel_policy.c native/rdp/channel_policy_test.c /src/nt-rdp/
COPY native/rdp/rdpdr_policy.h native/rdp/rdpdr_policy.c native/rdp/rdpdr_policy_test.c /src/nt-rdp/
RUN cc -std=c11 -Wall -Wextra -Werror -O2 -D_GNU_SOURCE /src/nt-rdp/channel_policy.c /src/nt-rdp/channel_policy_test.c -o /tmp/nt-channel-policy-test && /tmp/nt-channel-policy-test \
 && cc -std=c11 -Wall -Wextra -Werror -O2 -D_GNU_SOURCE /src/nt-rdp/rdpdr_policy.c /src/nt-rdp/rdpdr_policy_test.c -o /tmp/nt-rdpdr-policy-test && /tmp/nt-rdpdr-policy-test
RUN mkdir -p /opt/nt/bin && PKG_CONFIG_PATH=/opt/freerdp/lib/pkgconfig \
    sh -c 'cc -std=c11 -Wall -Wextra -Werror -Wno-deprecated-declarations -O2 $NT_EXTRA_CFLAGS /src/nt-rdp/worker.c /src/nt-rdp/capture.c /src/nt-rdp/channel_policy.c /src/nt-rdp/rdpdr_policy.c -o /opt/nt/bin/nt-rdp-worker $(pkg-config --cflags --libs freerdp-server-proxy3 freerdp3 winpr3 json-c openssl) -pthread -Wl,--export-dynamic-symbol=nt_rdpdr_back_gate,--export-dynamic-symbol=nt_rdpdr_front_gate -Wl,--disable-new-dtags,-rpath,/opt/freerdp/lib && cc -std=c11 -Wall -Wextra -Werror -Wno-deprecated-declarations -O2 $NT_EXTRA_CFLAGS /src/nt-rdp/probe_client.c -o /opt/nt/bin/nt-rdp-probe-client $(pkg-config --cflags --libs freerdp3 winpr3 openssl) -Wl,--disable-new-dtags,-rpath,/opt/freerdp/lib'
ENTRYPOINT ["/opt/nt/bin/nt-rdp-worker"]
