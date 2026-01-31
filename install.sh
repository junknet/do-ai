#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_NAME="do-ai"
DEST_DIR="${DEST_DIR:-$HOME/.local/bin}"

if ! command -v go >/dev/null 2>&1; then
  echo "未检测到 Go，请先安装 Go 再执行安装。" >&2
  exit 1
fi

mkdir -p "$DEST_DIR"

go build -trimpath -ldflags "-s -w" -o "$DEST_DIR/$BIN_NAME" "$ROOT_DIR/src"

cat <<INFO
安装完成：$DEST_DIR/$BIN_NAME
用法示例：
  $DEST_DIR/$BIN_NAME codex
  $DEST_DIR/$BIN_NAME claude code
  $DEST_DIR/$BIN_NAME gemini
INFO
