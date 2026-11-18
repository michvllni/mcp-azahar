# SPDX-License-Identifier: GPL-3.0-or-later
#
# Dev container for mcp-azahar: Go toolchain (via feature), Xvfb + Mesa
# software rendering, an extracted Azahar AppImage, golangci-lint, goreleaser.
#
# BASE_IMAGE matches the image the Coder workspace was running when the repo
# was bootstrapped (devcontainers base-debian 2.2.5, bookworm).
# KNOWN BLOCKER: Azahar 2127.0-rc3 needs glibc >= 2.39, bookworm has 2.36, so
# Azahar does not start on this base. See NOTES.md ("Blocker: glibc").
ARG BASE_IMAGE=mcr.microsoft.com/devcontainers/base:2.2.5-bookworm
FROM ${BASE_IMAGE}

ARG AZAHAR_VERSION=2127.0-rc3
ARG AZAHAR_SHA256=173dcedb78357c4920ddfca385fdce273e2e9e31b874564830068061f98fc1da
ARG GOLANGCI_LINT_VERSION=2.15.0
ARG GORELEASER_VERSION=2.20.2

# Xvfb + Mesa, Qt/XCB runtime deps, capture/inspection tools, and the libs
# Azahar's bundle expects from the host (found with ldd).
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      xvfb xauth mesa-utils libgl1-mesa-dri libegl1 libxkbcommon-x11-0 \
      libxcb-cursor0 libxcb-icccm4 libxcb-image0 libxcb-keysyms1 libxcb-randr0 \
      libxcb-render-util0 libxcb-shape0 libxcb-xinerama0 libxcb-xkb1 \
      libfontconfig1 libdbus-1-3 x11-utils imagemagick file jq unzip curl python3 \
      libopengl0 libharfbuzz0b libjack-jackd2-0 libasound2 libfribidi0 libusb-1.0-0 \
      squashfs-tools binutils ca-certificates \
 && rm -rf /var/lib/apt/lists/*

COPY install-azahar.sh install-go-tools.sh /tmp/devcontainer/
RUN /tmp/devcontainer/install-azahar.sh -t "${AZAHAR_VERSION}" -s "${AZAHAR_SHA256}" -d /opt/azahar \
 && /tmp/devcontainer/install-go-tools.sh -l "${GOLANGCI_LINT_VERSION}" -r "${GORELEASER_VERSION}" \
 && rm -rf /tmp/devcontainer

ENV AZAHAR_BIN=/opt/azahar/AppRun
