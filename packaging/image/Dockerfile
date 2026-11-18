# SPDX-License-Identifier: GPL-3.0-or-later
#
# Release image ghcr.io/michvllni/mcp-azahar (DECISIONS.md D10): mcp-azahar,
# Azahar (extracted AppImage), Xvfb and Mesa, so `launch` works headless
# without installing anything. Built by goreleaser (dockers_v2): the build
# context holds linux/amd64/mcp-azahar and the extra_files listed in
# .goreleaser.yaml, not the repository. Run with -i for MCP over stdio; do not
# publish UDP 45987 (D9).
# Full debian:trixie, not -slim: the slim image lacks libraries the AppImage
# needs (e.g. libcom_err.so.2); this base and package list are the ones the CI
# integration job runs Azahar with.
ARG BASE=debian:trixie@sha256:9c6e381421d0f48d1b8133e573f33c185ee16cbf505eda9c4a8a4791b25ea8a6

FROM ${BASE} AS azahar
ARG AZAHAR_VERSION
ARG AZAHAR_SHA256
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      ca-certificates curl binutils squashfs-tools \
 && rm -rf /var/lib/apt/lists/*
COPY .devcontainer/install-azahar.sh /tmp/install-azahar.sh
RUN /tmp/install-azahar.sh -t "${AZAHAR_VERSION}" -s "${AZAHAR_SHA256}" -d /opt/azahar

FROM ${BASE}
ARG TARGETPLATFORM
# Xvfb + Mesa and the host libraries Azahar's bundle expects (same list as
# .devcontainer/Dockerfile, without the development tools).
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      xvfb xauth libgl1-mesa-dri libegl1 libxkbcommon-x11-0 \
      libxcb-cursor0 libxcb-icccm4 libxcb-image0 libxcb-keysyms1 libxcb-randr0 \
      libxcb-render-util0 libxcb-shape0 libxcb-xinerama0 libxcb-xkb1 \
      libfontconfig1 libdbus-1-3 libopengl0 libharfbuzz0b libjack-jackd2-0 \
      libasound2t64 libfribidi0 libusb-1.0-0 \
 && rm -rf /var/lib/apt/lists/*
COPY --from=azahar /opt/azahar /opt/azahar
COPY ${TARGETPLATFORM}/mcp-azahar /usr/local/bin/mcp-azahar
COPY LICENSE /usr/share/doc/mcp-azahar/LICENSE
COPY packaging/image/NOTICE.md /usr/share/doc/mcp-azahar/NOTICE.md
COPY .release/azahar-source/azahar-LICENSE.txt /usr/share/doc/azahar/LICENSE.txt
RUN useradd --create-home --uid 1000 azahar && mkdir /work && chown azahar:azahar /work
USER azahar
WORKDIR /work
ENV AZAHAR_BIN=/opt/azahar/AppRun
ENTRYPOINT ["mcp-azahar"]
CMD ["serve"]
