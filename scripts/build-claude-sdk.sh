#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
sdk_root="$repo_root/runtime/claude-sdk"
npm --prefix "$sdk_root" ci --ignore-scripts --no-audit --no-fund
"$sdk_root/node_modules/.bin/esbuild" "$sdk_root/bridge.mjs" --bundle --platform=node \
  --format=esm --target=node20 --banner:js='import { createRequire as sdkCreateRequire } from "node:module"; const require = sdkCreateRequire(import.meta.url);' \
  --outfile="$repo_root/runtime/internal/claudep/sdk-runtime/bridge.mjs"
