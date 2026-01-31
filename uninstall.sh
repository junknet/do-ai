#!/usr/bin/env bash
set -euo pipefail

BIN_NAME="do-ai"
DEST_DIR="${DEST_DIR:-$HOME/.local/bin}"
TARGET="$DEST_DIR/$BIN_NAME"

if [ -f "$TARGET" ]; then
  rm -f "$TARGET"
  echo "已删除：$TARGET"
else
  echo "未找到：$TARGET"
fi

