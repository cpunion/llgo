// Investigation only. No runtime, test pressure, retry, or GC-policy changes.
import {spawn, spawnSync} from 'node:child_process';
import {createWriteStream, mkdirSync, readdirSync, copyFileSync, writeFileSync} from 'node:fs';
import {join} from 'node:path';
import {homedir} from 'node:os';

const [subject, evidence] = process.argv.slice(2);
if (!subject || !evidence) throw new Error('usage: diagnose_w1_macos.mjs subject evidence');
mkdirSync(evidence, {recursive: true});
const log = createWriteStream(join(evidence, 'native-tests.log'));
const processLog = createWriteStream(join(evidence, 'processes.log'));
const started = Date.now();
const child = spawn('bash', ['dev/test_go_version.sh', '1.27'], {
  cwd: subject, env: process.env, detached: true, stdio: ['ignore', 'pipe', 'pipe'],
});
child.stdout.on('data', data => {log.write(data); process.stdout.write(data);});
child.stderr.on('data', data => {log.write(data); process.stderr.write(data);});
let sampled = false;
let timedOut = false;
let closing = false;
function snapshot() {
  const ps = spawnSync('ps', ['-Ao', 'pid,ppid,etime,%cpu,rss,command'], {encoding: 'utf8', timeout: 10000});
  processLog.write(`\n${new Date().toISOString()}\n${ps.stdout || ps.stderr}`);
  // Sample only a long-running test/go process, once. Any sampled success is
  // diagnostic evidence, not an unperturbed final acceptance result.
  if (sampled) return;
  for (const line of (ps.stdout || '').split('\n')) {
    if (!/\/llgo-test-[^/]+\/go\.test(?:\s|$)/.test(line)) continue;
    const fields = line.trim().split(/\s+/);
    const duration = fields[2].split(':').map(Number);
    if (duration.length === 2 && duration[0] < 2) continue;
    const pid = fields[0];
    if (!/^\d+$/.test(pid)) continue;
    sampled = true;
    spawnSync('sample', [pid, '1', '1', '-file', join(evidence, 'go-test.sample.txt')], {timeout: 15000});
    break;
  }
}
snapshot();
const interval = setInterval(snapshot, 120000);
// This bounds a broken runtime timeout, rather than treating a hung trial as
// a passing retry. Only this harness's newly created process group is killed.
const deadline = setTimeout(() => {
  timedOut = true;
  snapshot();
  try {process.kill(-child.pid, 'SIGKILL');} catch (error) {
    if (error.code !== 'ESRCH') throw error;
  }
}, 30 * 60000);
child.on('error', error => {log.write(String(error)); finish(null, null, String(error));});
child.on('close', (code, signal) => finish(code, signal));
function finish(code, signal, error) {
  if (closing) return;
  closing = true;
  clearInterval(interval);
  clearTimeout(deadline);
  snapshot();
  const result = {code, signal, error, timedOut, sampled, seconds: (Date.now() - started) / 1000};
  writeFileSync(join(evidence, 'result.json'), JSON.stringify(result, null, 2) + '\n');
  console.log(JSON.stringify(result));
  for (const dir of [join(homedir(), 'Library/Logs/DiagnosticReports'), '/Library/Logs/DiagnosticReports']) {
    let names;
    try {names = readdirSync(dir);} catch {continue;}
    for (const name of names) {
      if (/^(go[._-]|llgo)/.test(name) && /\.(ips|crash)$/.test(name)) {
        try {copyFileSync(join(dir, name), join(evidence, name));} catch (error) {
          log.write(`Could not copy ${name}: ${error}\n`);
        }
      }
    }
  }
  log.end();
  processLog.end();
  process.exitCode = code === 0 && !timedOut && !error ? 0 : 1;
}
