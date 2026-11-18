# SPDX-License-Identifier: GPL-3.0-or-later
#
# Dev container for mcp-azahar: Go toolchain (via feature), Xvfb + Mesa
# software rendering, an extracted Azahar AppImage, golangci-lint, goreleaser,
# actionlint and shellcheck.
#
# Debian 13 trixie: Azahar 2127 AppImages need glibc >= 2.39 (trixie: 2.41)
# and a newer HarfBuzz than bookworm ships. Same image family/version as the
# bookworm image the Coder workspace started from (base-debian 2.2.5).
ARG BASE_IMAGE=mcr.microsoft.com/devcontainers/base:2.2.5-trixie
FROM ${BASE_IMAGE}

# Azahar is bumped by hand (version and digest together, see AGENTS.md).
# golangci-lint, goreleaser and actionlint versions live in
# install-go-tools.sh (single source for image and CI, updated by Renovate).
ARG AZAHAR_VERSION=2127.0-rc3
ARG AZAHAR_SHA256=173dcedb78357c4920ddfca385fdce273e2e9e31b874564830068061f98fc1da

# Xvfb + Mesa, Qt/XCB runtime deps, capture/inspection tools, and the libs
# Azahar's bundle expects from the host (found with ldd).
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      xvfb xauth mesa-utils libgl1-mesa-dri libegl1 libxkbcommon-x11-0 \
      libxcb-cursor0 libxcb-icccm4 libxcb-image0 libxcb-keysyms1 libxcb-randr0 \
      libxcb-render-util0 libxcb-shape0 libxcb-xinerama0 libxcb-xkb1 \
      libfontconfig1 libdbus-1-3 x11-utils imagemagick file jq unzip curl python3 \
      libopengl0 libharfbuzz0b libjack-jackd2-0 libasound2t64 libfribidi0 libusb-1.0-0 \
      squashfs-tools binutils ca-certificates shellcheck \
 && rm -rf /var/lib/apt/lists/*

COPY install-azahar.sh install-go-tools.sh /tmp/devcontainer/
RUN /tmp/devcontainer/install-azahar.sh -t "${AZAHAR_VERSION}" -s "${AZAHAR_SHA256}" -d /opt/azahar \
 && /tmp/devcontainer/install-go-tools.sh \
 && rm -rf /tmp/devcontainer

ENV AZAHAR_BIN=/opt/azahar/AppRun
