// Exercise the installed extension against real paused Wasm state. Node 22+
// supplies WebSocket; no separate browser automation dependency is needed.
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';

const [profile, sessionURL] = process.argv.slice(2);
const port = (await readFile(`${profile}/DevToolsActivePort`, 'utf8')).split('\n')[0];
const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
const pageTarget = targets.find(t => t.type === 'page' && t.url.startsWith(sessionURL));
const devtoolsTarget = targets.find(t => t.url.startsWith('chrome-extension://') && t.url.endsWith('/devtools.html'));
assert.ok(pageTarget && devtoolsTarget, JSON.stringify(targets));

async function connect(target) {
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  const pending = new Map(), listeners = new Map();
  let sequence = 0;
  socket.onmessage = ({data}) => {
    const message = JSON.parse(data);
    if (message.id) {
      const request = pending.get(message.id);
      pending.delete(message.id);
      if (message.error) request.reject(new Error(JSON.stringify(message.error)));
      else request.resolve(message.result);
    } else {
      for (const listener of listeners.get(message.method) || []) listener(message.params);
    }
  };
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  return {
    send(method, params = {}) {
      return new Promise((resolve, reject) => {
        const id = ++sequence;
        pending.set(id, {resolve, reject});
        socket.send(JSON.stringify({id, method, params}));
      });
    },
    on(method, callback) {
      if (!listeners.has(method)) listeners.set(method, []);
      listeners.get(method).push(callback);
    },
    close() { socket.close(); },
  };
}

async function until(probe, description) {
  for (let attempt = 0; attempt < 200; ++attempt) {
    const value = await probe();
    if (value) return value;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  throw new Error(`timed out: ${description}`);
}

const page = await connect(pageTarget);
const frontend = await connect(devtoolsTarget);
try {
  const contexts = [];
  frontend.on('Runtime.executionContextCreated', ({context}) => contexts.push(context));
  await frontend.send('Runtime.enable');
  const contextID = await until(async () => {
    for (const context of contexts) {
      const {result} = await frontend.send('Runtime.evaluate', {
        contextId: context.id, expression: 'typeof globalThis.__llgoLanguageExtensionPlugin',
      });
      if (result.value === 'object') return context.id;
    }
  }, 'extension execution context');
  async function extension(expression) {
    const result = await frontend.send('Runtime.evaluate', {
      contextId: contextID, expression, awaitPromise: true, returnByValue: true,
    });
    if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
    return result.result.value;
  }
  await extension(`(() => {
    const plugin = globalThis.__llgoLanguageExtensionPlugin;
    globalThis.__llgoEvaluations = [];
    const evaluate = plugin.evaluate.bind(plugin);
    plugin.evaluate = async (...args) => {
      try {
        const result = await evaluate(...args);
        globalThis.__llgoEvaluations.push({name: args[0], result});
        return result;
      } catch (error) {
        globalThis.__llgoEvaluations.push({name: args[0], error: String(error)});
        throw error;
      }
    };
  })()`);

  const index = await (await fetch(`${sessionURL}__llgo/debug-index.json`)).json();
  const fixture = index.sources.find(s => s.path.endsWith('/fixture.c'));
  const source = fixture || index.sources.find(s => s.path.endsWith('/_wrap/probe.cpp'));
  assert.ok(source, 'debug fixture source is indexed');
  const lineNumber = fixture ? 2 : 4; // Return after local initialization, zero-based.
  const mapping = await extension(`(async () => {
    const plugin = globalThis.__llgoLanguageExtensionPlugin;
    const [rawModuleId] = plugin.modules.keys();
    return plugin.sourceLocationToRawLocation({rawModuleId,
      sourceFileURL: ${JSON.stringify(new URL(source.url, sessionURL).href)},
      lineNumber: ${lineNumber}, columnNumber: 0});
  })()`);
  assert.ok(mapping.length, 'extension maps the source breakpoint');
  const scripts = [];
  let paused;
  page.on('Debugger.scriptParsed', script => scripts.push(script));
  page.on('Debugger.paused', event => { paused = event; });
  await page.send('Debugger.enable');
  const wasm = await until(() => scripts.find(s => s.scriptLanguage === 'WebAssembly'), 'Wasm script');
  const breakpoint = await page.send('Debugger.setBreakpoint', {location: {
    scriptId: wasm.scriptId, lineNumber: 0,
    columnNumber: mapping[0].startOffset + wasm.codeOffset,
  }});
  // Do not await execution: it will wait while the debugger is paused.
  const run = page.send('Runtime.evaluate', {expression: 'globalThis.__llgoDebugRun()', awaitPromise: true});
  await until(() => paused, 'source breakpoint');
  assert.ok(paused.hitBreakpoints.includes(breakpoint.breakpointId), JSON.stringify(paused));
  const expectedName = fixture ? 'value' : 'cpp_local';
  const expectedValue = fixture ? 42 : 21;
  let evaluations;
  try {
    await until(async () => {
      evaluations = JSON.parse(await extension('JSON.stringify(globalThis.__llgoEvaluations, (_, value) => typeof value === "bigint" ? String(value) : value)')); 
      return evaluations.some(e => e.name === expectedName && e.result?.value === expectedValue);
    }, `real paused ${expectedName}=${expectedValue}`);
  } catch (error) {
    throw new Error(`${error.message}; evaluations=${JSON.stringify(evaluations)}; pause=${JSON.stringify(paused.callFrames.slice(0, 2))}`);
  }
  await page.send('Debugger.removeBreakpoint', {breakpointId: breakpoint.breakpointId});
  await page.send('Debugger.resume');
  await run;
  if (fixture) {
    const {result} = await page.send('Runtime.evaluate', {
      expression: 'globalThis.__llgoDebugStatus', returnByValue: true,
    });
    assert.deepEqual(result.value, {state: 'exited', code: 0});
  } else {
    // An Emscripten fiber can keep the browser runtime alive after Go main
    // returns. Require the real Go completion sentinel without inventing exit.
    await until(async () => {
      const {result} = await page.send('Runtime.evaluate', {
        expression: '({status: globalThis.__llgoDebugStatus, output: document.getElementById("output").textContent})',
        returnByValue: true,
      });
      assert.notEqual(result.value.status.state, 'error', JSON.stringify(result.value));
      return result.value.output.includes('wasm debug ok');
    }, 'Go completion output');
  }
  console.log(`source breakpoint, ${expectedName}=${expectedValue}, and program completion passed`);
} finally {
  page.close();
  frontend.close();
}
