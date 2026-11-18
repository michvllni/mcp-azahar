<!-- SPDX-License-Identifier: GPL-3.0-or-later -->
# Notice: ghcr.io/michvllni/mcp-azahar

This image is an unofficial tool, not affiliated with the Azahar project.

- **mcp-azahar**: GPL-3.0-or-later (`/usr/share/doc/mcp-azahar/LICENSE`).
  Source: https://github.com/michvllni/mcp-azahar, at the tag of this image's
  version.
- **Azahar** (`/opt/azahar`, the official AppImage of the version in
  `/opt/azahar/AZAHAR_VERSION`, extracted): GPL-3.0
  (`/usr/share/doc/azahar/LICENSE.txt`). Its complete corresponding source,
  `azahar-unified-source-<version>.tar.xz` as published by Azahar, is attached
  to the mcp-azahar GitHub release of this image's version.
- **Qt 6.8.2** (bundled in the AppImage, `/opt/azahar/usr/lib/libQt6*`):
  LGPL-3.0 (`/usr/share/common-licenses/LGPL-3` and `GPL-3`). Source:
  https://download.qt.io/archive/qt/6.8/6.8.2/single/qt-everywhere-src-6.8.2.tar.xz.
  Other libraries in the AppImage are listed with their licenses in Azahar's
  source tarball (`externals/`) and at https://github.com/azahar-emu/azahar.
- **Debian packages** (Xvfb, Mesa and libraries): their licenses are in
  `/usr/share/doc/<package>/copyright`; sources at
  https://snapshot.debian.org for the versions installed.

Usage: `docker run -i --rm --user "$(id -u):$(id -g)" -v "$PWD:/work"
ghcr.io/michvllni/mcp-azahar` starts the MCP server over stdio; tools read and
write files under `/work`.
Do not publish UDP port 45987 or use `--network host`: Azahar's RPC server is
unauthenticated.
