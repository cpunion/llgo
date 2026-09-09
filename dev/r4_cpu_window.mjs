// Diagnostic-only preload: sample the main thread even while Wasm blocks its
// event loop. The helper worker runs no guest code and accesses no guest heap.
import { writeFileSync, writeSync } from 'node:fs';
import { Session } from 'node:inspector/promises';
import { Worker, isMainThread, parentPort, workerData } from 'node:worker_threads';

if (isMainThread) {
  const path = process.env.LLGO_DIAG_CPU_PROFILE;
  const milliseconds = Number(process.env.LLGO_DIAG_CPU_WINDOW_MS);
  if (!path || !Number.isSafeInteger(milliseconds) || milliseconds <= 0) {
    throw new Error('CPU sampling requires a profile path and positive window');
  }
  // Do not inherit --import or --cpu-prof into the sampling worker.
  const worker = new Worker(new URL(import.meta.url), {
    execArgv: [], workerData: { path, milliseconds },
  });
  await new Promise((resolve, reject) => {
    worker.once('error', reject);
    worker.once('exit', code => reject(new Error(`profiler exited before ready: ${code}`)));
    worker.once('message', message => message === 'ready'
      ? resolve() : reject(new Error(`unexpected profiler message: ${message}`)));
  });
  // A program that finishes normally must not be kept alive by this diagnostic.
  // --cpu-prof saves that complete run; the worker saves a bounded partial run.
  worker.unref();
} else {
  // Pending inspector promises do not themselves keep a worker event loop alive.
  const keepAlive = setInterval(() => {}, 1000);
  const session = new Session();
  session.connectToMainThread();
  await session.post('Profiler.enable');
  await session.post('Profiler.start');
  parentPort.postMessage('ready');
  setTimeout(async () => {
    const { profile } = await session.post('Profiler.stop');
    writeFileSync(workerData.path, JSON.stringify(profile));
    clearInterval(keepAlive);
    writeSync(2, `Saved bounded CPU sample after ${workerData.milliseconds} ms; this is not an acceptance result.\n`);
    // Stop only this diagnostic Node process after the profile is durable.
    process.kill(process.pid, 'SIGTERM');
  }, workerData.milliseconds);
}
