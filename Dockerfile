ARG BUILD_FROM

# --- BARESIP BUILD
# NOTE: baresip has ready-to-use docker images, see https://github.com/baresip/docker
# However these images are Debian based and not musl-based, so we cannot use them.
# Note also that there is a a baresip package in Alpine, but as of June 2025 it's available only in "edge/testing":
# https://pkgs.alpinelinux.org/package/edge/testing/x86_64/baresip
# That's why we build baresip from source here.
#
# NOTE: alpine:3.20 is used because as of June 2025, 
#         https://github.com/home-assistant/builder/blob/master/build.yaml  points to "amd64-base:3.20"
#         docker run -ti ghcr.io/home-assistant/amd64-base:3.20 cat etc/os-release
#       shows that the base OS is alpine:3.20.
FROM alpine:3.20 AS baresip-builder
ENV VERSION=v3.23.0
WORKDIR /root/

# install basic build dependencies
RUN apk add build-base make cmake pkgconf git 

# libre dependencies:
RUN apk add openssl-dev wget ca-certificates linux-headers zlib-dev

# first build the libre dependency:
RUN git clone -b ${VERSION} --depth=1 https://github.com/baresip/re.git 
RUN cd re && \
    cmake -B build -DCMAKE_BUILD_TYPE=Release -DCMAKE_C_FLAGS="-g" && \
    cmake --build build -j4 && \
    cmake --install build --prefix dist && cp -a dist/* /usr/

# baresip dependencies
RUN apk add opus-dev fdk-aac-dev alsa-lib-dev opencore-amr-dev pulseaudio-dev spandsp-dev tiff-dev

# then build the baresip binary:
RUN git clone -b ${VERSION} --depth=1 https://github.com/baresip/baresip.git && \
    cd baresip && \
    cmake -B build -DCMAKE_BUILD_TYPE=Release -DCMAKE_C_FLAGS="-g" -DCMAKE_CXX_FLAGS="-g" -DCMAKE_INSTALL_PREFIX=/usr && \
    cmake --build build -j4 && \
    cmake --install build --prefix dist && cp -a dist/* /usr/

# copy everthing into /root/dist/usr/ to simplify the copy in the final layer
RUN mkdir -p /root/dist/usr && \
    cp -a /root/re/dist/* /root/dist/usr/ && \
    cp -a /root/baresip/dist/* /root/dist/usr/

# --- BACKEND BUILD
# About base image: we need to use a musl-based docker image since the actual HomeAssistant addon
# base image will be musl-based as well and we need to ensure binary compatibility between he
# builder layer and the actual addon layer.
FROM golang:1.24-alpine AS backend-builder

# build go backend
WORKDIR /app/backend
COPY backend .
RUN --mount=type=cache,target=/root/.cache/apk \
    apk add build-base
RUN --mount=type=cache,target=/root/.cache/go \
    CGO_ENABLED=1 go build -o /backend .

# download frontend dependencies
#WORKDIR /app/frontend
#COPY frontend .
#RUN apk add yarn bash && \
#    yarn


# --- Actual ADDON layer

FROM $BUILD_FROM

# Add env
ENV LANG=C.UTF-8

# Setup base
#RUN apk add --no-cache nginx-debug sqlite socat && mv /etc/nginx /etc/nginx-orig
#RUN apk add baresip

# Copy data
COPY rootfs /
#COPY config.yaml /opt/bin/addon-config.yaml

# Copy web frontend HTML, CSS and JS files
#COPY frontend/*.html /opt/web/templates/
#COPY --from=builder /app/frontend/external-libs/*.js /opt/web/static/
#COPY --from=builder /app/frontend/external-libs/*.css /opt/web/static/
#COPY --from=builder /app/frontend/libs/*.css /opt/web/static/
#COPY frontend/libs/*.js /opt/web/static/
#COPY frontend/images/*.png /opt/web/static/

# Copy baresip binary
COPY --from=baresip-builder /root/dist/usr/ /usr/

# Copy backend binary
COPY --from=backend-builder /backend /opt/bin/

LABEL org.opencontainers.image.source=https://github.com/f18m/ha-addon-voip-client
