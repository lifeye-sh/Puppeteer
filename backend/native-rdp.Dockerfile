# Development SDK only: this image does not expose a native RDP listener.
# The platform authorization/recording worker must be implemented before release.
FROM golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS sdk
RUN apk add --no-cache build-base cmake ninja pkgconf curl openssl-dev zlib-dev \
    libjpeg-turbo-dev libpng-dev json-c-dev krb5-dev icu-dev linux-headers
WORKDIR /src
RUN curl --fail --location --retry 3 https://codeload.github.com/FreeRDP/FreeRDP/tar.gz/refs/tags/3.32.2 -o freerdp.tar.gz \
 && echo '9ccb8357bf0188044a460de775c7ea289ca7d2a2a917eca0f0a7c4dc9c8b08ea  freerdp.tar.gz' | sha256sum -c - \
 && tar -xzf freerdp.tar.gz && rm freerdp.tar.gz
RUN cmake -S FreeRDP-3.32.2 -B build -G Ninja -DCMAKE_BUILD_TYPE=Release \
    -DCMAKE_INSTALL_PREFIX=/opt/freerdp -DWITH_SERVER=ON -DWITH_PROXY=ON \
    -DWITH_SHADOW=OFF -DWITH_SAMPLE=OFF -DWITH_X11=OFF -DWITH_WAYLAND=OFF \
    -DWITH_SDL=OFF -DWITH_CLIENT_SDL=OFF -DWITH_CLIENT=ON -DWITH_CLIENT_COMMON=ON \
    -DWITH_FFMPEG=OFF -DWITH_DSP_FFMPEG=OFF -DWITH_VIDEO_FFMPEG=OFF -DWITH_SWSCALE=OFF \
    -DWITH_CUPS=OFF -DWITH_PULSE=OFF -DWITH_ALSA=OFF -DWITH_PCSC=OFF \
    -DWITH_OSS=OFF -DWITH_MANPAGES=OFF -DWITH_FUSE=OFF -DCHANNEL_URBDRC=OFF -DBUILD_TESTING=OFF \
 && cmake --build build --parallel 2 && cmake --install build
ENV PATH="/opt/freerdp/bin:${PATH}"
ENV LD_LIBRARY_PATH="/opt/freerdp/lib"
CMD ["/bin/sh"]
