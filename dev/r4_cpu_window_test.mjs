import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import test from 'node:test';

const preload = new URL('./r4_cpu_window.mjs', import.meta.url).pathname;

function sample(body, milliseconds) {
  const dir = mkdtempSync(join(tmpdir(), 'llgo-cpu-window-'));
  try {
    const run = spawnSync(process.execPath, [
      '--cpu-prof', `--cpu-prof-dir=${dir}`, '--cpu-prof-name=completed.cpuprofile',
      '--import', preload, '-e', body,
    ], {
      encoding: 'utf8', timeout: 10_000,
      env: { ...process.env, LLGO_DIAG_CPU_PROFILE: join(dir, 'window.cpuprofile'),
        LLGO_DIAG_CPU_WINDOW_MS: String(milliseconds) },
    });
    assert.ifError(run.error);
    if (run.signal === null) assert.equal(run.status, 0, run.stderr);
    const name = run.signal ? 'window.cpuprofile' : 'completed.cpuprofile';
    const profile = JSON.parse(readFileSync(join(dir, name), 'utf8'));
    assert.ok(profile.samples.length > 0);
    assert.equal(profile.samples.length, profile.timeDeltas.length);
    return { run, profile };
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

test('samples a blocked main thread before terminating the diagnostic', () => {
  const { run, profile } = sample('function spin() { while (true) {} } spin();', 250);
  assert.equal(run.signal, 'SIGTERM');
  assert.match(run.stderr, /not an acceptance result/);
  assert.ok(profile.nodes.some(n => n.callFrame.functionName === 'spin'));
});

test('does not delay or terminate a program that completes normally', () => {
  const { run } = sample('console.log("finished");', 5000);
  assert.equal(run.status, 0);
  assert.equal(run.signal, null);
  assert.equal(run.stdout.trim(), 'finished');
  assert.doesNotMatch(run.stderr, /bounded CPU sample/);
});
