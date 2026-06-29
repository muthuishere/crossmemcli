#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ "${GITHUB_ACTIONS:-}" != "true" || -z "${ACTIONS_ID_TOKEN_REQUEST_TOKEN:-}" ]]; then
  echo "error: npm OIDC publish must run inside GitHub Actions with id-token: write" >&2
  exit 1
fi

publish_package() {
  local dir="$1"
  local name version

  name="$(node -p "require('${dir}/package.json').name")"
  version="$(node -p "require('${dir}/package.json').version")"

  if npm view "${name}@${version}" version >/dev/null 2>&1; then
    echo "skip ${name}@${version}: already published"
    return 0
  fi

  echo "publish ${name}@${version}"
  (cd "$dir" && npm publish --access public --provenance)
}

publish_package "${ROOT_DIR}/npm/platforms/darwin-arm64"
publish_package "${ROOT_DIR}/npm/platforms/darwin-x64"
publish_package "${ROOT_DIR}/npm/platforms/linux-arm64"
publish_package "${ROOT_DIR}/npm/platforms/linux-x64"
publish_package "${ROOT_DIR}/npm/platforms/windows-x64"
publish_package "${ROOT_DIR}/npm"
