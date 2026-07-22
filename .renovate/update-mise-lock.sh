#!/usr/bin/env bash
set -euo pipefail
curl -fsSL https://mise.jdx.dev/install.sh | MISE_INSTALL_PATH=/tmp/mise sh
/tmp/mise install
