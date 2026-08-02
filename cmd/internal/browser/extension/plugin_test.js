const assert = require('node:assert/strict');
const test = require('node:test');

require('./plugin.js');

const {LLGoLanguageExtensionPlugin} = globalThis.LLGoLanguageExtension;

function uleb(value) {
  const result = [];
  do {
    let current = value & 0x7f;
    value >>>= 7;
    if (value) current |= 0x80;
    result.push(current);
  } while (value);
  return result;
}

function custom(name, payload) {
  const nameBytes = [...new TextEncoder().encode(name)];
  const contents = [...uleb(nameBytes.length), ...nameBytes, ...payload];
  return [0, ...uleb(contents.length), ...contents];
}

function moduleBytes({marker = true, id = [1, 2, 3, 4]} = {}) {
  const result = [0, 0x61, 0x73, 0x6d, 1, 0, 0, 0];
  if (marker) {
    result.push(...custom('llgo.debugger', [
      0x4c, 0x4c, 0x47, 0x4f, 0x44, 0x42, 0x47, 0,
      1, 1, 1, 1, 2, 4, 1, 0,
    ]));
  }
  result.push(...custom('build_id', [...uleb(id.length), ...id]));
  return new Uint8Array(result);
}

function fixtureIndex() {
  return {
    contract: 'llgo.browser.debug', version: 1, build_id: '01020304', artifact: 'embedded',
    record: {
      record_version: 1, schema_version: 1, runtime_layout_version: 1,
      llgo_abi_version: 1, cabi_mode: 2, pointer_size: 4, byte_order: 1,
    },
    sources: [{id: 'source', path: '/src/main.go', url: '/__llgo/source/source', local: true}],
    lines: [{source: 'source', line: 7, column: 1, start: 10, end: 20}],
    functions: [{name: 'main.main', ranges: [{start: 10, end: 20}]}],
    variables: [
      {name: 'constant', scope: 'LOCAL', type: 'int32', depth: 2, ranges: [{start: 10, end: 20}],
       constant: {kind: 'signed', value: '41'}},
      {name: 'local', scope: 'LOCAL', type: 'int32', depth: 2, ranges: [{start: 10, end: 20}],
       locations: [{expression: 'ed00009f'}]},
    ],
    types: [{id: 'int32', name: 'int32', kind: 'integer', size: 4, signed: true, complete: true}],
  };
}

const schema = {
  schema_version: 1, runtime_layout_version: 1, llgo_abi_version: 1,
  runtime_layouts: {'1': {
    string: {type_name: 'string', data: 'data', length: 'len'},
    slice: {type_pattern: '^\\[\\].+', data: 'data', length: 'len', capacity: 'cap'},
  }},
};

function response(value, init) {
  return new Response(typeof value === 'string' ? value : JSON.stringify(value), init);
}

test('LLGo language extension maps DWARF index and evaluates constants and Wasm locals', async () => {
  const calls = [];
  const services = {
    getWasmLocal: async (index, stopId) => {
      calls.push(['local', index, stopId]);
      return {type: 'i32', value: 42};
    },
    getWasmGlobal: async () => { throw new Error('unexpected global'); },
    getWasmOp: async () => { throw new Error('unexpected operand'); },
    getWasmLinearMemory: async () => { throw new Error('unexpected memory'); },
  };
  const fetcher = async (url, options = {}) => {
    if (String(url).endsWith('/__llgo/debug-index.json')) return response(fixtureIndex());
    if (String(url).endsWith('/__llgo/debug-schema.json')) return response(schema);
    if (String(url).endsWith('/__llgo/plugin-ready')) {
      calls.push(['ready']);
      return new Response(null, {status: 204});
    }
    throw new Error(`unexpected URL ${url}`);
  };
  const plugin = new LLGoLanguageExtensionPlugin(services, fetcher);
  const rawModule = {url: 'http://127.0.0.1:1234/program.wasm', code: moduleBytes().buffer};
  const sources = await plugin.addRawModule('module', undefined, rawModule);
  assert.deepEqual(sources, ['http://127.0.0.1:1234/__llgo/source/source']);
  assert.deepEqual(await plugin.sourceLocationToRawLocation({
    rawModuleId: 'module', sourceFileURL: sources[0], lineNumber: 7, columnNumber: 0,
  }), [{rawModuleId: 'module', startOffset: 10, endOffset: 20}]);
  assert.deepEqual(await plugin.rawLocationToSourceLocation({
    rawModuleId: 'module', codeOffset: 12, inlineFrameIndex: 0,
  }), [{rawModuleId: 'module', sourceFileURL: sources[0], lineNumber: 7, columnNumber: 1}]);
  assert.deepEqual(await plugin.getMappedLines('module', sources[0]), [7]);
  assert.deepEqual(await plugin.getFunctionInfo({rawModuleId: 'module', codeOffset: 12, inlineFrameIndex: 0}), {
    frames: [{name: 'main.main'}], missingSymbolFiles: [],
  });
  assert.deepEqual((await plugin.listVariablesInScope({rawModuleId: 'module', codeOffset: 12})).map(v => v.name),
                   ['constant', 'local']);
  assert.deepEqual(await plugin.evaluate('constant', {rawModuleId: 'module', codeOffset: 12}, 'stop'), {
    type: 'number', value: 41, description: '41', hasChildren: false,
  });
  assert.deepEqual(await plugin.evaluate('local', {rawModuleId: 'module', codeOffset: 12}, 'stop'), {
    type: 'number', value: 42, description: '42', hasChildren: false,
  });
  assert.deepEqual(calls, [['ready'], ['local', 0, 'stop']]);
});

test('LLGo language extension reports missing symbols and ignores non-LLGo modules', async () => {
  const services = {};
  const fetcher = async url => {
    if (String(url).endsWith('/__llgo/debug-index.json')) {
      return response({missing_symbol_files: ['http://host/missing.wasm']}, {status: 424});
    }
    throw new Error(`unexpected URL ${url}`);
  };
  const plugin = new LLGoLanguageExtensionPlugin(services, fetcher);
  const missing = await plugin.addRawModule('missing', 'http://host/missing.wasm', {
    url: 'http://host/program.wasm', code: moduleBytes().buffer,
  });
  assert.deepEqual(missing, {missingSymbolFiles: ['http://host/missing.wasm']});

  const ignored = await plugin.addRawModule('plain', undefined, {
    url: 'http://host/plain.wasm', code: moduleBytes({marker: false}).buffer,
  });
  assert.deepEqual(ignored, []);
});

test('LLGo language extension presents strings, slices, aggregates, and children from linear memory', async () => {
  const index = fixtureIndex();
  index.types.push(
      {id: 'uint32', name: 'uint32', kind: 'integer', size: 4, complete: true},
      {id: 'int32ptr', name: '*int32', kind: 'pointer', size: 4, elem: 'int32', complete: true},
      {id: 'string', name: 'string', kind: 'struct', size: 8, complete: true,
       fields: [{name: 'data', type: 'int32ptr', offset: 0}, {name: 'len', type: 'uint32', offset: 4}]},
      {id: 'slice', name: '[]int32', kind: 'struct', size: 12, complete: true,
       fields: [{name: 'data', type: 'int32ptr', offset: 0}, {name: 'len', type: 'uint32', offset: 4},
                {name: 'cap', type: 'uint32', offset: 8}]},
      {id: 'pair', name: 'main.Pair', kind: 'struct', size: 8, complete: true,
       fields: [{name: 'Left', type: 'int32', offset: 0}, {name: 'Right', type: 'int32', offset: 4}]},
  );
  index.variables.push(
      {name: 'text', scope: 'LOCAL', type: 'string', depth: 2, ranges: [{start: 10, end: 20}],
       locations: [{expression: '0310000000'}]},
      {name: 'values', scope: 'LOCAL', type: 'slice', depth: 2, ranges: [{start: 10, end: 20}],
       locations: [{expression: '0320000000'}]},
      {name: 'pair', scope: 'LOCAL', type: 'pair', depth: 2, ranges: [{start: 10, end: 20}],
       locations: [{expression: '0340000000'}]},
  );
  const memory = new Uint8Array(256);
  const view = new DataView(memory.buffer);
  view.setUint32(16, 100, true);
  view.setUint32(20, 3, true);
  memory.set(new TextEncoder().encode('abc'), 100);
  view.setUint32(32, 120, true);
  view.setUint32(36, 2, true);
  view.setUint32(40, 3, true);
  view.setInt32(64, 9, true);
  view.setInt32(68, 10, true);
  view.setInt32(120, 7, true);
  view.setInt32(124, 8, true);
  const services = {
    getWasmLinearMemory: async (offset, length) => memory.slice(offset, offset + length).buffer,
  };
  const fetcher = async url => {
    if (String(url).endsWith('/__llgo/debug-index.json')) return response(index);
    if (String(url).endsWith('/__llgo/debug-schema.json')) return response(schema);
    if (String(url).endsWith('/__llgo/plugin-ready')) return new Response(null, {status: 204});
    throw new Error(`unexpected URL ${url}`);
  };
  const plugin = new LLGoLanguageExtensionPlugin(services, fetcher);
  const context = {rawModuleId: 'module', codeOffset: 12};
  await plugin.addRawModule('module', undefined, {
    url: 'http://127.0.0.1:1234/program.wasm', code: moduleBytes().buffer,
  });
  assert.deepEqual(await plugin.evaluate('text', context, 'stop'), {
    type: 'string', className: 'string', value: 'abc', description: '"abc"', hasChildren: false,
    linearMemoryAddress: 100, linearMemorySize: 3,
  });
  const values = await plugin.evaluate('values', context, 'stop');
  assert.equal(values.description, '[]int32 len=2 cap=3');
  assert.deepEqual((await plugin.getProperties(values.objectId)).map(item => [item.name, item.value.value]),
                   [['0', 7], ['1', 8]]);
  assert.deepEqual(await plugin.evaluate('pair.Right', context, 'stop'), {
    type: 'number', value: 10, description: '10', hasChildren: false,
  });
});

test('LLGo language extension rejects stale browser indexes', async () => {
  const index = fixtureIndex();
  index.build_id = 'ffffffff';
  const fetcher = async url => {
    if (String(url).endsWith('/__llgo/debug-index.json')) return response(index);
    throw new Error(`unexpected URL ${url}`);
  };
  const plugin = new LLGoLanguageExtensionPlugin({}, fetcher);
  await assert.rejects(plugin.addRawModule('stale', undefined, {
    url: 'http://host/program.wasm', code: moduleBytes().buffer,
  }), /build_id mismatch/);
});
