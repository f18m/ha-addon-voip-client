all: build-docker-image

#
# BUILD targets
#

# non-containerized build of the backend -- requires you to have go installed:
build-backend:
	@echo "Assuming GO is already installed -- see https://golang.org/doc/install if that's not the case"
	cd backend && \
		go build -o bin/backend . 
	@echo "Assuming golangci-lint is already installed -- see https://golangci-lint.run/usage/install/#installing-golangci-lint if that's not the case"
	cd backend && \
		golangci-lint run
	cd backend && \
		go test -v -cover ./...

# fmt-backend:
# 	cd backend && \
# 		go fmt ./...
# 	# required by the gofumpt linter:
# 	cd backend && \
# 		gofumpt -l -w -extra .

# build-frontend:
# 	@echo "Assuming YARN is already installed -- see https://yarnpkg.com/getting-started/install if that's not the case"
# 	cd frontend/ && \
# 		yarn
# 	@echo "Assuming SASS is already installed -- see https://sass-lang.com/install if that's not the case"
# 	# transpile the SCSS -> CSS
# 	cd frontend && \
# 		sass scss/dnsmasq-dhcp.scss libs/dnsmasq-dhcp.css

# DART_SASS_VERSION=1.87.0
# #DART_ARCH:=linux-x64-musl
# DART_ARCH:=linux-x64

# install-dart-sass:
# 	rm -rf dart-sass
# 	wget https://github.com/sass/dart-sass/releases/download/$(DART_SASS_VERSION)/dart-sass-$(DART_SASS_VERSION)-$(DART_ARCH).tar.gz && \
# 		tar -xzf dart-sass-$(DART_SASS_VERSION)-$(DART_ARCH).tar.gz && \
# 		rm dart-sass-$(DART_SASS_VERSION)-$(DART_ARCH).tar.gz 
# 	dart-sass/sass --version
# 	dart-sass/sass frontend/scss/dnsmasq-dhcp.scss frontend/libs/dnsmasq-dhcp.css

# INPUT_SCSS:=$(shell pwd)/frontend/scss/
# OUTPUT_CSS:=$(shell pwd)/frontend

# build-css-live:
# 	docker run -v $(INPUT_SCSS):/sass/ -v $(OUTPUT_CSS):/css/ -it michalklempa/dart-sass:latest


#
# DOCKER targets
#

# NOTE: the architecture "armhf" (ARM v6) is excluded from the list because Go toolchain is not available there
ARCH:=--armv7 --amd64 --aarch64 --i386
ifeq ($(FAST),1)
# pick just 1 arch instead of all, to speedup
ARCH:=--amd64
endif
IMAGETAG:=$(shell yq .image config.yaml  | sed 's@{arch}@amd64@g')

BACKEND_SOURCE_CODE_FILES:=$(shell find backend/ -type f -name '*.go')
ROOTFS_FILES:=$(shell find rootfs/ -type f)

HOME_ASSISTANT_BUILDER_VERSION:=2025.03.0

build-docker-image: $(BACKEND_SOURCE_CODE_FILES) $(ROOTFS_FILES)
	docker run \
		--rm \
		--privileged \
		-v ~/.docker:/root/.docker \
		-v /var/run/docker.sock:/var/run/docker.sock:ro \
		-v $(shell pwd):/data \
		ghcr.io/home-assistant/amd64-builder:$(HOME_ASSISTANT_BUILDER_VERSION) \
		$(ARCH) \
		--target /data \
		--version localtest \
		--self-cache \
		--test

build-docker-image-raw:
	# do not use the HomeAssistant builder -- this helps debugging some docker build issues
	# see https://github.com/home-assistant/builder/blob/master/build.yaml
	sudo docker build \
		--build-arg BUILD_FROM=ghcr.io/home-assistant/amd64-base:3.20 \
		-t $(IMAGETAG):localtest \
		.

TEST_CONTAINER_NAME:=voip-client-test
DOCKER_RUN_OPTIONS:= \
	-v $(shell pwd)/test-options.json:/data/options.json \
	-v $(shell pwd)/config.yaml:/opt/bin/addon-config.yaml \
	-v $(shell pwd)/backend:/app \
	-e LOCAL_TESTING=1

# when using the 'test-docker-image' target it's normal to see messages like
#    "Something went wrong contacting the API"
# at startup of the docker container... the reason is that the startup scripts
# will try to reach to HomeAssistant Supervisor which is not running...
test-docker-image: 
	$(MAKE) FAST=1 build-docker-image
	@echo
	@echo "Starting container of image $(IMAGETAG):localtest" 
	@echo "Point your browser at http://localhost:8976"
	@echo
	@echo "Starting container of image ${IMAGETAG}:localtest" 
	docker run \
		--rm \
		--name $(TEST_CONTAINER_NAME) \
		${DOCKER_RUN_OPTIONS} \
		${IMAGETAG}:localtest

# # NOTE: in the HTTP link below the port is actually the one in test-options.json, and currently it's 8976
# test-docker-image-live: 
# 	sudo docker build -f Dockerfile.live -t debug-image-live .
# 	@echo
# 	@echo "Starting container of image debug-image-live" 
# 	@echo "Point your browser at http://localhost:8976"
# 	@echo
# 	docker run \
# 		--rm \
# 		--name $(TEST_CONTAINER_NAME) \
# 		${DOCKER_RUN_OPTIONS} \
# 		debug-image-live

