// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Licensed under the Apache License, Version 2.0.

(() => {
  'use strict';

  const INDEX_CONTRACT = 'llgo.browser.debug';
  const INDEX_VERSION = 1;
  const RECORD_MAGIC = [0x4c, 0x4c, 0x47, 0x4f, 0x44, 0x42, 0x47, 0x00];
  const RECORD_SIZE = 16;
  const MAX_CHILDREN = 100;

  function readULEB(bytes, cursor) {
    let value = 0n;
    let shift = 0n;
    for (let count = 0; count < 10; ++count) {
      if (cursor.offset >= bytes.length) throw new Error('truncated varuint');
      const current = bytes[cursor.offset++];
      value |= BigInt(current & 0x7f) << shift;
      if ((current & 0x80) === 0) return value;
      shift += 7n;
    }
    throw new Error('invalid varuint');
  }

  function readSLEB(bytes, cursor) {
    let value = 0n;
    let shift = 0n;
    let current = 0;
    for (let count = 0; count < 10; ++count) {
      if (cursor.offset >= bytes.length) throw new Error('truncated varint');
      current = bytes[cursor.offset++];
      value |= BigInt(current & 0x7f) << shift;
      shift += 7n;
      if ((current & 0x80) === 0) {
        if ((current & 0x40) !== 0) value |= (-1n) << shift;
        return value;
      }
    }
    throw new Error('invalid varint');
  }

  function readName(bytes, cursor) {
    const size = Number(readULEB(bytes, cursor));
    if (cursor.offset + size > bytes.length) throw new Error('truncated WebAssembly name');
    const value = new TextDecoder().decode(bytes.subarray(cursor.offset, cursor.offset + size));
    cursor.offset += size;
    return value;
  }

  function customSections(moduleBytes) {
    const bytes = moduleBytes instanceof Uint8Array ? moduleBytes : new Uint8Array(moduleBytes);
    if (bytes.length < 8 || bytes[0] !== 0 || bytes[1] !== 0x61 || bytes[2] !== 0x73 || bytes[3] !== 0x6d) {
      throw new Error('invalid WebAssembly module');
    }
    const result = new Map();
    const cursor = {offset: 8};
    while (cursor.offset < bytes.length) {
      const id = bytes[cursor.offset++];
      const size = Number(readULEB(bytes, cursor));
      const end = cursor.offset + size;
      if (end > bytes.length) throw new Error('truncated WebAssembly section');
      if (id === 0) {
        const name = readName(bytes.subarray(0, end), cursor);
        if (result.has(name)) throw new Error(`multiple ${name} custom sections`);
        result.set(name, bytes.slice(cursor.offset, end));
      }
      cursor.offset = end;
    }
    return result;
  }

  function debuggerRecord(sections) {
    const bytes = sections.get('llgo.debugger');
    if (!bytes) return null;
    if (bytes.length !== RECORD_SIZE) throw new Error('invalid LLGo debugger record size');
    for (let index = 0; index < RECORD_MAGIC.length; ++index) {
      if (bytes[index] !== RECORD_MAGIC[index]) throw new Error('invalid LLGo debugger record magic');
    }
    if (bytes[15] !== 0) throw new Error('invalid LLGo debugger record reserved byte');
    return {
      record_version: bytes[8],
      schema_version: bytes[9],
      runtime_layout_version: bytes[10],
      llgo_abi_version: bytes[11],
      cabi_mode: bytes[12],
      pointer_size: bytes[13],
      byte_order: bytes[14],
    };
  }

  function buildID(sections) {
    const bytes = sections.get('build_id');
    if (!bytes) return null;
    const cursor = {offset: 0};
    const size = Number(readULEB(bytes, cursor));
    if (size !== bytes.length - cursor.offset) throw new Error('invalid WebAssembly build_id');
    return [...bytes.subarray(cursor.offset)].map(value => value.toString(16).padStart(2, '0')).join('');
  }

  function inRanges(offset, ranges) {
    return !ranges || ranges.length === 0 || ranges.some(range =>
      ((!range.start && !range.end) || (offset >= range.start && offset < range.end)));
  }

  function bytesFromHex(value) {
    if (value.length % 2 !== 0) throw new Error('invalid hexadecimal DWARF expression');
    const result = new Uint8Array(value.length / 2);
    for (let index = 0; index < result.length; ++index) {
      result[index] = Number.parseInt(value.slice(index * 2, index * 2 + 2), 16);
    }
    return result;
  }

  function sameRecord(left, right) {
    return left.record_version === right.record_version &&
        left.schema_version === right.schema_version &&
        left.runtime_layout_version === right.runtime_layout_version &&
        left.llgo_abi_version === right.llgo_abi_version &&
        left.cabi_mode === right.cabi_mode &&
        left.pointer_size === right.pointer_size && left.byte_order === right.byte_order;
  }

  class LLGoLanguageExtensionPlugin {
    constructor(languageServices, fetcher = globalThis.fetch.bind(globalThis)) {
      this.languageServices = languageServices;
      this.fetcher = fetcher;
      this.modules = new Map();
      this.objects = new Map();
      this.nextObject = 1;
      this.schema = null;
      this.addRawModuleCalls = 0;
      this.lastAddRawModuleError = null;
    }

    async addRawModule(rawModuleId, symbolsURL, rawModule) {
      ++this.addRawModuleCalls;
      try {
        return await this.loadRawModule(rawModuleId, symbolsURL, rawModule);
      } catch (error) {
        this.lastAddRawModuleError = String(error && error.stack || error);
        throw error;
      }
    }

    async loadRawModule(rawModuleId, symbolsURL, rawModule) {
      let code = rawModule.code;
      if (!code) {
        const response = await this.fetcher(rawModule.url);
        if (!response.ok) throw new Error(`load WebAssembly module: HTTP ${response.status}`);
        code = await response.arrayBuffer();
      }
      const sections = customSections(new Uint8Array(code));
      const record = debuggerRecord(sections);
      // A dedicated LLGo session must leave non-LLGo modules usable as raw
      // WebAssembly instead of claiming source presentation for them.
      if (!record) return [];
      const id = buildID(sections);
      if (!id) throw new Error('LLGo WebAssembly module has no build_id');

      const indexURL = new URL('/__llgo/debug-index.json', rawModule.url).href;
      const response = await this.fetcher(indexURL, {cache: 'no-store'});
      if (response.status === 424 || response.status === 404) {
        let missing = symbolsURL ? [symbolsURL] : [];
        try {
          const details = await response.json();
          if (Array.isArray(details.missing_symbol_files)) missing = details.missing_symbol_files;
        } catch (_) {
        }
        return {missingSymbolFiles: missing};
      }
      if (!response.ok) throw new Error(`load LLGo browser debug index: HTTP ${response.status}`);
      const index = await response.json();
      if (index.contract !== INDEX_CONTRACT || index.version !== INDEX_VERSION) {
        throw new Error(`unsupported LLGo browser debug index ${index.contract || '<missing>'} v${index.version}`);
      }
      if (index.build_id !== id) throw new Error('LLGo browser debug index build_id mismatch');
      if (!sameRecord(record, index.record)) throw new Error('LLGo browser debug index record mismatch');
      const schema = await this.loadSchema(rawModule.url);
      if (record.schema_version !== schema.schema_version ||
          record.runtime_layout_version !== schema.runtime_layout_version ||
          record.llgo_abi_version !== schema.llgo_abi_version) {
        throw new Error(`unsupported LLGo debugger schema/runtime/ABI ${record.schema_version}/${record.runtime_layout_version}/${record.llgo_abi_version}`);
      }

      const sources = new Map();
      const sourceByURL = new Map();
      for (const source of index.sources) {
        const resolved = {...source, resolvedURL: new URL(source.url, rawModule.url).href};
        sources.set(source.id, resolved);
        sourceByURL.set(resolved.resolvedURL, resolved);
      }
      const types = new Map(index.types.map(type => [type.id, type]));
      const runtimeLayouts = schema.runtime_layouts || {};
      const layout = runtimeLayouts[String(record.runtime_layout_version)] || null;
      this.modules.set(rawModuleId, {
        rawModuleId, rawModule, symbolsURL, record, index, sources, sourceByURL, types, layout,
      });
      const ready = await this.fetcher(new URL('/__llgo/plugin-ready', rawModule.url).href, {
        cache: 'no-store',
      });
      if (!ready.ok) throw new Error(`report LLGo browser debugger readiness: HTTP ${ready.status}`);
      return [...sources.values()].filter(source => source.local).map(source => source.resolvedURL);
    }

    async loadSchema(moduleURL) {
      if (!this.schema) {
        const schemaURL = new URL('/__llgo/debug-schema.json', moduleURL).href;
        const response = await this.fetcher(schemaURL, {cache: 'no-store'});
        if (!response.ok) throw new Error(`load LLGo debugger schema: HTTP ${response.status}`);
        this.schema = await response.json();
      }
      return this.schema;
    }

    async removeRawModule(rawModuleId) {
      this.modules.delete(rawModuleId);
      for (const [id, object] of this.objects) {
        if (object.rawModuleId === rawModuleId) this.objects.delete(id);
      }
    }

    module(rawModuleId) {
      const module = this.modules.get(rawModuleId);
      if (!module) throw new Error(`unknown LLGo raw module ${rawModuleId}`);
      return module;
    }

    async sourceLocationToRawLocation(location) {
      const module = this.module(location.rawModuleId);
      const source = module.sourceByURL.get(location.sourceFileURL);
      if (!source) return [];
      return module.index.lines
          .filter(line => line.source === source.id && line.line === location.lineNumber)
          .map(line => ({rawModuleId: location.rawModuleId, startOffset: line.start, endOffset: line.end}));
    }

    async rawLocationToSourceLocation(location) {
      const module = this.module(location.rawModuleId);
      return module.index.lines.filter(line => location.codeOffset >= line.start && location.codeOffset < line.end)
          .map(line => {
            const source = module.sources.get(line.source);
            return source ? {
              rawModuleId: location.rawModuleId,
              sourceFileURL: source.resolvedURL,
              lineNumber: line.line,
              columnNumber: line.column,
            } : null;
          }).filter(Boolean);
    }

    async getMappedLines(rawModuleId, sourceFileURL) {
      const module = this.module(rawModuleId);
      const source = module.sourceByURL.get(sourceFileURL);
      if (!source) return undefined;
      return [...new Set(module.index.lines.filter(line => line.source === source.id).map(line => line.line))]
          .sort((left, right) => left - right);
    }

    async getScopeInfo(type) {
      const names = {GLOBAL: 'Global', LOCAL: 'Local', PARAMETER: 'Parameter'};
      if (!names[type]) throw new Error(`unknown LLGo scope ${type}`);
      return {type, typeName: names[type], icon: 'data:null'};
    }

    activeVariables(module, codeOffset) {
      const active = module.index.variables.filter(variable => inRanges(codeOffset, variable.ranges) &&
          (variable.constant || variable.locations.some(location => inRanges(codeOffset, [location]))));
      active.sort((left, right) => right.depth - left.depth);
      const names = new Set();
      return active.filter(variable => {
        if (names.has(variable.name)) return false;
        names.add(variable.name);
        return true;
      });
    }

    async listVariablesInScope(location) {
      const module = this.module(location.rawModuleId);
      return this.activeVariables(module, location.codeOffset).map(variable => ({
        scope: variable.scope,
        name: variable.name,
        type: module.types.get(variable.type)?.name || '<unknown>',
      }));
    }

    async getFunctionInfo(location) {
      const module = this.module(location.rawModuleId);
      const matches = module.index.functions.filter(fn => inRanges(location.codeOffset, fn.ranges));
      matches.sort((left, right) => rangeWidth(left.ranges) - rangeWidth(right.ranges));
      return {frames: matches.map(fn => ({name: fn.name})), missingSymbolFiles: []};
    }

    async getInlinedFunctionRanges() { return []; }
    async getInlinedCalleesRanges() { return []; }

    async evaluate(expression, context, stopId) {
      const path = expression.trim().split('.').filter(Boolean);
      if (path.length === 0) return null;
      const module = this.module(context.rawModuleId);
      const variable = this.activeVariables(module, context.codeOffset).find(item => item.name === path[0]);
      if (!variable) return null;
      let type = module.types.get(variable.type);
      if (!type) return null;
      let located;
      if (variable.constant) {
        located = {kind: 'value', value: constantToValue(variable.constant)};
      } else {
        const location = variable.locations.find(item => inRanges(context.codeOffset, [item]));
        if (!location) return null;
        located = await this.evaluateDWARF(bytesFromHex(location.expression), module, stopId);
        if (!located) return null;
      }
      for (const fieldName of path.slice(1)) {
        const resolved = resolveType(module, type);
        if (located.kind !== 'address' || !resolved.fields) return null;
        const field = resolved.fields.find(item => item.name === fieldName);
        if (!field) return null;
        located = {kind: 'address', value: located.value + BigInt(field.offset)};
        type = module.types.get(field.type);
        if (!type) return null;
      }
      return this.remoteObject(module, type, located, stopId);
    }

    async evaluateDWARF(bytes, module, stopId) {
      const cursor = {offset: 0};
      const stack = [];
      let stackValue = false;
      const pop = () => {
        if (!stack.length) throw new Error('invalid empty DWARF expression stack');
        return stack.pop();
      };
      while (cursor.offset < bytes.length) {
        const op = bytes[cursor.offset++];
        if (op >= 0x30 && op <= 0x4f) {
          stack.push(BigInt(op - 0x30));
          continue;
        }
        switch (op) {
          case 0x03: stack.push(readFixed(bytes, cursor, module.record.pointer_size, false)); break; // DW_OP_addr
          case 0x06: {
            const address = pop();
            stack.push(await this.readUnsigned(address, module.record.pointer_size, stopId));
            break;
          }
          case 0x08: stack.push(readFixed(bytes, cursor, 1, false)); break;
          case 0x09: stack.push(readFixed(bytes, cursor, 1, true)); break;
          case 0x0a: stack.push(readFixed(bytes, cursor, 2, false)); break;
          case 0x0b: stack.push(readFixed(bytes, cursor, 2, true)); break;
          case 0x0c: stack.push(readFixed(bytes, cursor, 4, false)); break;
          case 0x0d: stack.push(readFixed(bytes, cursor, 4, true)); break;
          case 0x0e: stack.push(readFixed(bytes, cursor, 8, false)); break;
          case 0x0f: stack.push(readFixed(bytes, cursor, 8, true)); break;
          case 0x10: stack.push(readULEB(bytes, cursor)); break;
          case 0x11: stack.push(readSLEB(bytes, cursor)); break;
          case 0x12: stack.push(stack[stack.length - 1]); break;
          case 0x13: pop(); break;
          case 0x14: stack.push(stack[stack.length - 2]); break;
          case 0x1a: { const right = pop(); stack.push(pop() & right); break; }
          case 0x1b: { const right = pop(); stack.push(pop() / right); break; }
          case 0x1c: { const right = pop(); stack.push(pop() - right); break; }
          case 0x1d: { const right = pop(); stack.push(pop() % right); break; }
          case 0x1e: { const right = pop(); stack.push(pop() * right); break; }
          case 0x21: { const right = pop(); stack.push(pop() | right); break; }
          case 0x22: { const right = pop(); stack.push(pop() + right); break; }
          case 0x23: stack.push(pop() + readULEB(bytes, cursor)); break;
          case 0x24: { const right = pop(); stack.push(pop() << right); break; }
          case 0x25: { const right = pop(); stack.push(pop() >> right); break; }
          case 0x27: { const right = pop(); stack.push(pop() ^ right); break; }
          case 0x9f: stackValue = true; break;
          case 0xed: {
            if (cursor.offset >= bytes.length) throw new Error('truncated DW_OP_WASM_location');
            const kind = bytes[cursor.offset++];
            const index = kind === 3 ? Number(readFixed(bytes, cursor, 4, false)) : Number(readULEB(bytes, cursor));
            let wasm;
            if (kind === 0) wasm = await this.languageServices.getWasmLocal(index, stopId);
            else if (kind === 1 || kind === 3) wasm = await this.languageServices.getWasmGlobal(index, stopId);
            else if (kind === 2) wasm = await this.languageServices.getWasmOp(index, stopId);
            else throw new Error(`unsupported DW_OP_WASM_location kind ${kind}`);
            if (wasm.type === 'reftype') return null;
            stack.push(typeof wasm.value === 'bigint' ? wasm.value : BigInt(Math.trunc(wasm.value)));
            break;
          }
          default:
            throw new Error(`unsupported DWARF expression opcode 0x${op.toString(16)}`);
        }
      }
      if (stack.length !== 1) throw new Error('invalid DWARF expression result');
      return {kind: stackValue ? 'value' : 'address', value: stack[0]};
    }

    async remoteObject(module, originalType, located, stopId) {
      const type = resolveType(module, originalType);
      if (!type) return null;
      if (located.kind === 'value' && isScalar(type)) return scalarRemote(type, located.value);
      const address = located.value;
      if (isScalar(type)) {
        const value = await this.readScalar(type, address, stopId);
        return scalarRemote(type, value);
      }
      if (type.kind === 'pointer') {
        const pointer = located.kind === 'value' ? address : await this.readUnsigned(address, type.size, stopId);
        const object = this.storeObject(module, type, pointer, stopId, 'pointer');
        return {
          type: 'object', className: type.name, description: pointer === 0n ? 'nil' : `0x${pointer.toString(16)}`,
          objectId: object, hasChildren: pointer !== 0n,
          linearMemoryAddress: numberAddress(pointer), linearMemorySize: 0,
        };
      }
      const stringSpec = module.layout?.string;
      if (stringSpec && type.name === stringSpec.type_name) {
        return this.stringRemote(module, type, address, stopId, stringSpec);
      }
      const sliceSpec = module.layout?.slice;
      if (sliceSpec && new RegExp(sliceSpec.type_pattern).test(type.name)) {
        return this.sliceRemote(module, type, address, stopId, sliceSpec);
      }
      const objectId = this.storeObject(module, type, address, stopId, 'aggregate');
      return {
        type: type.kind === 'array' ? 'array' : 'object', className: type.name,
        description: type.name, objectId, hasChildren: true,
        linearMemoryAddress: numberAddress(address), linearMemorySize: Math.max(0, type.size),
      };
    }

    async stringRemote(module, type, address, stopId, spec) {
      const dataField = type.fields.find(field => field.name === spec.data);
      const lengthField = type.fields.find(field => field.name === spec.length);
      if (!dataField || !lengthField) return null;
      const pointer = await this.readUnsigned(address + BigInt(dataField.offset), module.record.pointer_size, stopId);
      const length = await this.readUnsigned(address + BigInt(lengthField.offset), module.record.pointer_size, stopId);
      const size = Number(length > 4096n ? 4096n : length);
      const raw = size ? await this.languageServices.getWasmLinearMemory(numberAddress(pointer), size, stopId) : new ArrayBuffer(0);
      const text = new TextDecoder().decode(raw);
      const truncated = length > 4096n ? '…' : '';
      return {
        type: 'string', className: type.name, value: text,
        description: JSON.stringify(text + truncated), hasChildren: false,
        linearMemoryAddress: numberAddress(pointer), linearMemorySize: Number(length),
      };
    }

    async sliceRemote(module, type, address, stopId, spec) {
      const dataField = type.fields.find(field => field.name === spec.data);
      const lengthField = type.fields.find(field => field.name === spec.length);
      const capacityField = type.fields.find(field => field.name === spec.capacity);
      if (!dataField || !lengthField || !capacityField) return null;
      const pointer = await this.readUnsigned(address + BigInt(dataField.offset), module.record.pointer_size, stopId);
      const length = await this.readUnsigned(address + BigInt(lengthField.offset), module.record.pointer_size, stopId);
      const capacity = await this.readUnsigned(address + BigInt(capacityField.offset), module.record.pointer_size, stopId);
      const objectId = this.storeObject(module, type, address, stopId, 'slice', {pointer, length});
      return {
        type: 'array', className: type.name, description: `${type.name} len=${length} cap=${capacity}`,
        objectId, hasChildren: length !== 0n,
        linearMemoryAddress: numberAddress(pointer), linearMemorySize: 0,
      };
    }

    storeObject(module, type, address, stopId, kind, extra = {}) {
      const id = `llgo:${this.nextObject++}`;
      this.objects.set(id, {rawModuleId: module.rawModuleId, type, address, stopId, kind, ...extra});
      return id;
    }

    async getProperties(objectId) {
      const object = this.objects.get(objectId);
      if (!object) return [];
      const module = this.module(object.rawModuleId);
      const type = resolveType(module, object.type);
      if (object.kind === 'pointer') {
        if (object.address === 0n) return [];
        const elem = module.types.get(type.elem);
        return [{name: '*', value: await this.remoteObject(module, elem, {kind: 'address', value: object.address}, object.stopId)}];
      }
      if (object.kind === 'slice') {
        const dataField = type.fields.find(field => field.name === module.layout.slice.data);
        const pointerType = dataField ? resolveType(module, module.types.get(dataField.type)) : null;
        const elem = pointerType?.elem ? module.types.get(pointerType.elem) : null;
        if (!elem || elem.size <= 0) return [];
        const count = Number(object.length > BigInt(MAX_CHILDREN) ? BigInt(MAX_CHILDREN) : object.length);
        const result = [];
        for (let index = 0; index < count; ++index) {
          result.push({
            name: String(index),
            value: await this.remoteObject(module, elem, {
              kind: 'address', value: object.pointer + BigInt(index) * BigInt(elem.size),
            }, object.stopId),
          });
        }
        return result;
      }
      if (type.kind === 'array') {
        const elem = module.types.get(type.elem);
        if (!elem || elem.size <= 0) return [];
        const count = Math.min(type.count, MAX_CHILDREN);
        const result = [];
        for (let index = 0; index < count; ++index) {
          result.push({name: String(index), value: await this.remoteObject(module, elem, {
            kind: 'address', value: object.address + BigInt(index) * BigInt(elem.size),
          }, object.stopId)});
        }
        return result;
      }
      const result = [];
      for (const field of type.fields || []) {
        const fieldType = module.types.get(field.type);
        if (!fieldType) continue;
        result.push({name: field.name, value: await this.remoteObject(module, fieldType, {
          kind: 'address', value: object.address + BigInt(field.offset),
        }, object.stopId)});
      }
      return result;
    }

    async releaseObject(objectId) { this.objects.delete(objectId); }

    async readUnsigned(address, size, stopId) {
      const raw = await this.languageServices.getWasmLinearMemory(numberAddress(address), size, stopId);
      const bytes = new Uint8Array(raw);
      let result = 0n;
      for (let index = 0; index < bytes.length; ++index) result |= BigInt(bytes[index]) << BigInt(index * 8);
      return result;
    }

    async readScalar(type, address, stopId) {
      const raw = await this.languageServices.getWasmLinearMemory(numberAddress(address), type.size, stopId);
      const view = new DataView(raw);
      if (type.kind === 'float') return type.size === 4 ? view.getFloat32(0, true) : view.getFloat64(0, true);
      let value = 0n;
      const bytes = new Uint8Array(raw);
      for (let index = 0; index < bytes.length; ++index) value |= BigInt(bytes[index]) << BigInt(index * 8);
      return signedValue(type, value);
    }
  }

  function resolveType(module, type) {
    const seen = new Set();
    while (type && (type.kind === 'typedef' || type.kind === 'qualified')) {
      if (seen.has(type.id)) return null;
      seen.add(type.id);
      type = module.types.get(type.elem);
    }
    return type;
  }

  function isScalar(type) {
    return ['bool', 'integer', 'float', 'enum'].includes(type.kind);
  }

  function signedValue(type, value) {
    if (!type.signed || type.size <= 0) return value;
    const bits = BigInt(type.size * 8);
    const sign = 1n << (bits - 1n);
    return (value & sign) !== 0n ? value - (1n << bits) : value;
  }

  function scalarRemote(type, raw) {
    if (type.kind === 'bool') {
      const value = raw !== 0n && raw !== 0;
      return {type: 'boolean', value, description: String(value), hasChildren: false};
    }
    if (typeof raw === 'number') {
      return {type: 'number', value: raw, description: String(raw), hasChildren: false};
    }
    const value = signedValue(type, raw);
    if (value <= BigInt(Number.MAX_SAFE_INTEGER) && value >= BigInt(Number.MIN_SAFE_INTEGER)) {
      return {type: 'number', value: Number(value), description: value.toString(), hasChildren: false};
    }
    return {type: 'bigint', value, description: `${value}n`, hasChildren: false};
  }

  function constantToValue(constant) {
    if (constant.kind === 'signed' || constant.kind === 'unsigned') return BigInt(constant.value || '0');
    return 0n;
  }

  function readFixed(bytes, cursor, size, signed) {
    if (cursor.offset + size > bytes.length) throw new Error('truncated fixed-width DWARF operand');
    let result = 0n;
    for (let index = 0; index < size; ++index) result |= BigInt(bytes[cursor.offset++]) << BigInt(index * 8);
    if (signed) {
      const bits = BigInt(size * 8);
      const sign = 1n << (bits - 1n);
      if ((result & sign) !== 0n) result -= 1n << bits;
    }
    return result;
  }

  function numberAddress(value) {
    if (value < 0n || value > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error(`invalid linear-memory address ${value}`);
    return Number(value);
  }

  function rangeWidth(ranges) {
    return (ranges || []).reduce((total, range) => total + Math.max(0, range.end - range.start), 0);
  }

  globalThis.LLGoLanguageExtension = {
    LLGoLanguageExtensionPlugin,
    customSections,
    debuggerRecord,
    buildID,
    bytesFromHex,
  };

  if (globalThis.chrome?.devtools?.languageServices) {
    const languageServices = globalThis.chrome.devtools.languageServices;
    const plugin = new LLGoLanguageExtensionPlugin(languageServices);
    globalThis.__llgoLanguageExtensionPlugin = plugin;
    globalThis.__llgoLanguageExtensionRegistration = languageServices.registerLanguageExtensionPlugin(
        plugin, 'LLGo WebAssembly Debugger',
        {language: 'WebAssembly', symbol_types: ['EmbeddedDWARF', 'ExternalDWARF']})
        .then(() => new Promise(resolve => {
          // A Wasm module that is instantiated before DevTools has installed the
          // language plugin may not be offered to the plugin. Tell the inspected
          // LLGo launcher that registration is complete before it instantiates.
          chrome.devtools.inspectedWindow.eval(
              `globalThis.__llgoLanguageExtensionReady = true;
               globalThis.dispatchEvent(new Event('__llgoLanguageExtensionReady'));`,
              () => resolve());
        }))
        .catch(error => {
          console.error('LLGo language extension registration failed', error);
          throw error;
        });
  }
})();
