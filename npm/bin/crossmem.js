#!/usr/bin/env node
'use strict';

const { spawn } = require('child_process');
const { SUPPORTED, platformPackage, resolveBinary } = require('./resolve');

function fail(message) {
  process.stderr.write(`crossmem: ${message}\n`);
  process.exit(1);
}

const pkg = platformPackage();
if (!pkg) {
  fail(
    `unsupported platform ${process.platform}-${process.arch}. ` +
      `Supported: ${Object.keys(SUPPORTED).join(', ')}`,
  );
}

const binPath = resolveBinary();
if (!binPath) {
  fail(
    `platform package ${pkg} is not installed. Reinstall with: ` +
      `npm install -g @muthuishere/crossmem`,
  );
}

const child = spawn(binPath, process.argv.slice(2), { stdio: 'inherit' });
child.on('exit', (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 1);
});
child.on('error', (err) => fail(`failed to launch ${binPath}: ${err.message}`));
