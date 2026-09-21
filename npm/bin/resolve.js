'use strict';

const SUPPORTED = {
  'darwin-arm64': '@muthuishere/crossmem-darwin-arm64',
  'darwin-x64': '@muthuishere/crossmem-darwin-x64',
  'linux-arm64': '@muthuishere/crossmem-linux-arm64',
  'linux-x64': '@muthuishere/crossmem-linux-x64',
  'win32-x64': '@muthuishere/crossmem-windows-x64',
};

function platformPackage() {
  return SUPPORTED[`${process.platform}-${process.arch}`];
}

// Returns the prebuilt binary's path, or null when this platform has no
// package or the package is not installed.
function resolveBinary() {
  const pkg = platformPackage();
  if (!pkg) {
    return null;
  }
  const binName = process.platform === 'win32' ? 'crossmem.exe' : 'crossmem';
  try {
    return require.resolve(`${pkg}/bin/${binName}`);
  } catch (_err) {
    return null;
  }
}

module.exports = { SUPPORTED, platformPackage, resolveBinary };
