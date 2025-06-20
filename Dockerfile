ARG BUILD_FROM

# --- BACKEND BUILD
# About base image: we need to use a musl-based docker image since the actual HomeAssistant addon
# base image will be musl-based as well and we need to ensure binary compatibility between he
# builder layer and the actual addon layer.
FROM golang:1.24-alpine AS builder

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

# Copy backend binary
COPY --from=builder /backend /opt/bin/

LABEL org.opencontainers.image.source=https://github.com/f18m/ha-addon-voip-client
