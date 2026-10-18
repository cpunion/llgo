# Version-pinned causal experiment; writes ONLY the collector's mmap hint in
# the treatment arm. This is a diagnostic mutation, never a production fix.
import gdb,os,pathlib,json,hashlib,struct,time
out=pathlib.Path(os.environ['DIAG_OUT']);out.mkdir(exist_ok=True,parents=True)
image=pathlib.Path(os.environ['DIAG_IMAGE'])
assert hashlib.sha256(image.read_bytes()).hexdigest()=='e6f276e8309521c8b5b56c94979bc39702e4f74bf9ebac10fdd6189a52561e6e'
for cmd in ['set pagination off','set confirm off','set startup-with-shell off','set disable-randomization off','set print thread-events off','handle SIGPWR nostop noprint pass','handle SIGXCPU nostop noprint pass','handle SIGSEGV nostop noprint pass','handle SIGBUS nostop noprint pass','set environment GOMAXPROCS 2','set environment GOMEMLIMIT 2GiB']:
 gdb.execute(cmd)
gdb.execute('file '+str(image)); bp=gdb.Breakpoint('*0x38b734',internal=True); last=None

def event(e):
 global last
 last=e
gdb.events.stop.connect(event);gdb.events.exited.connect(event)
def u64(inf,a):return struct.unpack('<Q',inf.read_memory(a,8))[0]
def live_handles(inf):
 result=[];seen=set()
 for n in range(64):
  h=u64(inf,0x4b1450+n*8)
  while h and h not in seen:
   seen.add(h);key,live,nxt=struct.unpack('<QQQ',inf.read_memory(h,24))
   if live:result.append({'handle':h,'target':(~key)&((1<<64)-1)})
   h=nxt
 return result
rows=[]; matched={'no_write':0,'clear_only_mmap_hint':0}
for iteration in range(1,2001):
 mode='no_write' if matched['no_write']<5 else 'clear_only_mmap_hint'
 bp.enabled=True;bp.ignore_count=8;last=None;start=time.monotonic()
 transcript=gdb.execute('run',to_string=True)
 if isinstance(last,gdb.ExitedEvent):continue
 if not isinstance(last,gdb.BreakpointEvent):raise RuntimeError(transcript)
 inf=gdb.selected_inferior();live=live_handles(inf)
 # GC v8.0.6 Ubuntu22 build ID052089... debug symbol last_addr.0 @74a18,
 # GC_arrays @32ac0; verified independently by the downloaded ddeb symbols.
 hint_address=int(gdb.parse_and_eval('(unsigned long)&GC_arrays'))+0x41f58
 hint=u64(inf,hint_address);targets={h['target'] for h in live}
 try:c,init,state=struct.unpack('<QQQ',inf.read_memory(hint,24))
 except gdb.MemoryError:c,init,state=0,0,0
 if len(live)!=2 or hint not in targets or c not in targets or init!=1 or not state:
  gdb.execute('kill',to_string=True);continue
 row={'iteration':iteration,'mode':mode,'live_handles_before':live,'hint_address':hint_address,'hint_before':hint,'channel':c,'state':state,'before_seconds':time.monotonic()-start}
 if mode=='clear_only_mmap_hint':inf.write_memory(hint_address,struct.pack('<Q',0))
 row['hint_after']=u64(inf,hint_address);bp.enabled=False;last=None
 transcript+=gdb.execute('continue',to_string=True)
 row['event']=type(last).__name__;row['exit']=last.exit_code if isinstance(last,gdb.ExitedEvent) else None
 row['seconds']=time.monotonic()-start
 (out/('%s-%02d.txt'%(mode,matched[mode]+1))).write_text(transcript)
 rows.append(row);matched[mode]+=1;print('CAUSAL',json.dumps(row),flush=True)
 if row['exit']!=(2 if mode=='no_write' else 0):raise RuntimeError('Causal expected outcome not observed')
 if all(n>=5 for n in matched.values()):break
(out/'hint-causal-result.json').write_text(json.dumps({'rows':rows,'matched':matched,'policy':'No-write controls vs treatment clearing exactly collector mmap hint after repeated retention. Timer/channel refs, managers, program image and assertions unchanged. Debugger intervention is not a shipped fix.'},indent=2))
assert all(n>=5 for n in matched.values())
