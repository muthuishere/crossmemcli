#!/usr/bin/env node
'use strict';

// Installing the CLI installs the agent skill: the skill is how Claude Code,
// Codex and the rest discover crossmem in the first place. This never fails
// the npm install — a missing platform package or an unwritable home
// directory just means the user runs `crossmem install --skills` themselves.

const { spawnSync } = require('child_process');
const { resolveBinary } = require('./resolve');

if (process.env.CROSSMEM_NO_SKILL_INSTALL === '1') {
  process.exit(0);
}

const binPath = resolveBinary();
if (!binPath) {
  process.exit(0);
}

const result = spawnSync(binPath, ['install', '--skills', '--agents'], { stdio: 'inherit' });
if (result.error) {
  process.stderr.write(
    `crossmem: skill install skipped (${result.error.message}). ` +
      'Run: crossmem install --skills --agents\n',
  );
}
process.exit(0);
