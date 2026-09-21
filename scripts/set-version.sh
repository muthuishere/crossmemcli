#!/usr/bin/env bash
# Write one version into every npm package: the root launcher, its
# optionalDependencies pins, and each platform package. The Go binary takes its
# version from the git tag via GoReleaser ldflags, so nothing else to change.
set -euo pipefail

VERSION="${1:-}"
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "usage: set-version.sh <x.y.z>  (no leading v)" >&2
  exit 1
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="$VERSION" node - "$ROOT_DIR" <<'NODE'
const fs = require('fs');
const path = require('path');
const root = process.argv[2];
const version = process.env.VERSION;

const write = (file, mutate) => {
  const pkg = JSON.parse(fs.readFileSync(file, 'utf8'));
  mutate(pkg);
  fs.writeFileSync(file, JSON.stringify(pkg, null, 2) + '\n');
  console.log(`${pkg.name} -> ${pkg.version}`);
};

write(path.join(root, 'npm/package.json'), (pkg) => {
  pkg.version = version;
  for (const dep of Object.keys(pkg.optionalDependencies ?? {})) {
    pkg.optionalDependencies[dep] = version;
  }
});

for (const dir of fs.readdirSync(path.join(root, 'npm/platforms'))) {
  write(path.join(root, 'npm/platforms', dir, 'package.json'), (pkg) => {
    pkg.version = version;
  });
}
NODE
