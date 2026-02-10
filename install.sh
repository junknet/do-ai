#!/usr/bin/env bash
set -euo pipefail

BIN_NAME="do-ai"
DEST_DIR="${DEST_DIR:-$HOME/.local/bin}"
REPO_ARCHIVE_URL="${DO_AI_REPO_ARCHIVE_URL:-https://github.com/junknet/do-ai/archive/refs/heads/main.tar.gz}"
ECS_SSH="${DO_AI_ECS_SSH:-}"
ECS_PATHS="${DO_AI_ECS_PATHS:-${DO_AI_ECS_PATH:-}}"

cleanup_dir=""
win_build_dir=""
cleanup() {
  if [[ -n "$win_build_dir" && -d "$win_build_dir" ]]; then
    rm -rf "$win_build_dir"
  fi
  if [[ -n "$cleanup_dir" && -d "$cleanup_dir" ]]; then
    rm -rf "$cleanup_dir"
  fi
}
trap cleanup EXIT

if ! command -v go >/dev/null 2>&1; then
  echo "未检测到 Go，请先安装 Go 再执行安装。" >&2
  exit 1
fi

ROOT_DIR=""
if [[ -n "${BASH_SOURCE[0]:-}" && -f "${BASH_SOURCE[0]}" ]]; then
  ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
  cleanup_dir="$(mktemp -d)"
  curl -fsSL "$REPO_ARCHIVE_URL" | tar -xz -C "$cleanup_dir"
  ROOT_DIR="$cleanup_dir/do-ai-main"
fi

mkdir -p "$DEST_DIR"

(
  cd "$ROOT_DIR"
  go build -trimpath -ldflags "-s -w" -o "$DEST_DIR/$BIN_NAME" ./src
)

# 可选：同步更新 ECS（仅当设置了 DO_AI_ECS_SSH 与 DO_AI_ECS_PATHS/DO_AI_ECS_PATH）
if [[ -n "$ECS_SSH" && -n "$ECS_PATHS" ]]; then
  win_build_dir="$(mktemp -d)"
  win_exe="$win_build_dir/do-ai.exe"
  (
    cd "$ROOT_DIR"
    GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o "$win_exe" ./src
  )
  IFS=';' read -r -a ecs_targets <<< "$ECS_PATHS"
  for target in "${ecs_targets[@]}"; do
    target="$(echo "$target" | xargs)"
    if [[ -z "$target" ]]; then
      continue
    fi
    scp "$win_exe" "${ECS_SSH}:${target}"
  done
  echo "ECS 已更新：$ECS_SSH -> $ECS_PATHS"
else
  echo "未设置 DO_AI_ECS_SSH/DO_AI_ECS_PATHS，已跳过 ECS 更新。"
fi

cat <<INFO
安装完成：$DEST_DIR/$BIN_NAME
用法示例：
  $DEST_DIR/$BIN_NAME codex
  $DEST_DIR/$BIN_NAME claude code
  $DEST_DIR/$BIN_NAME gemini
INFO
