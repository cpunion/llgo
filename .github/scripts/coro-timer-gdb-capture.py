# Dynamic diagnostic only: inspect the collector's mark source after eight failed
# fixture collection rounds. No calls into the inferior, and no fix claim.
import gdb,os,pathlib,json,hashlib,struct,time
out=pathlib.Path(os.environ['DIAG_OUT']);out.mkdir(exist_ok=True,parents=True)
image=pathlib.Path(os.environ['DIAG_IMAGE'])
assert hashlib.sha256(image.read_bytes()).hexdigest()=='e6f276e8309521c8b5b56c94979bc39702e4f74bf9ebac10fdd6189a52561e6e'
for cmd in ['set pagination off','set confirm off','set startup-with-shell off','set disable-randomization off','set print thread-events off','handle SIGPWR nostop noprint pass','handle SIGXCPU nostop noprint pass','handle SIGSEGV nostop noprint pass','handle SIGBUS nostop noprint pass','set environment GOMAXPROCS 2','set environment GOMEMLIMIT 2GiB']:
 gdb.execute(cmd)
gdb.execute('file '+str(image))
bp=gdb.Breakpoint('*0x38b734',internal=True)
last=None
def event(e):
 global last
 last=e
gdb.events.stop.connect(event);gdb.events.exited.connect(event)
def u64(inf,a):return struct.unpack('<Q',inf.read_memory(a,8))[0]
def handles(inf):
 result=[];seen=set()
 for n in range(64):
  h=u64(inf,0x4b1450+n*8)
  while h and h not in seen:
   seen.add(h);key,live,nxt=struct.unpack('<QQQ',inf.read_memory(h,24))
   if live:result.append({'handle':h,'key':key,'target':(~key)&((1<<64)-1)})
   h=nxt
 return result
def mappings(inf):return pathlib.Path('/proc/%d/maps'%inf.pid).read_text()
def regions(inf):
 for line in mappings(inf).splitlines():
  p=line.split()
  if not p[1].startswith('rw'):continue
  lo,hi=[int(x,16) for x in p[0].split('-')]
  if hi-lo>64*1024**2:continue
  try:yield lo,bytes(inf.read_memory(lo,hi-lo))
  except gdb.MemoryError:continue
rows=[];found=False
for iteration in range(1,2001):
 bp.enabled=True;bp.ignore_count=8;last=None
 transcript=gdb.execute('run',to_string=True)
 row={'iteration':iteration,'event':type(last).__name__};rows.append(row)
 if isinstance(last,gdb.ExitedEvent):
  row['exit']=last.exit_code
  if iteration%50==0:print('SEARCH PASS',iteration,flush=True)
  continue
 if not isinstance(last,gdb.BreakpointEvent):
  row['unexpected_stop']=transcript;gdb.execute('kill',to_string=True);break
 inf=gdb.selected_inferior();live=handles(inf);targets={h['target'] for h in live};pairs=[]
 for h in live:
  try:
   c,init,state=struct.unpack('<QQQ',inf.read_memory(h['target'],24))
   if c in targets and init==1 and state:pairs.append((h,c,state))
  except gdb.MemoryError:pass
 row['live_handles']=live
 if len(pairs)!=1:
  row['selection_error']='expected unique live Timer/channel pair';gdb.execute('kill',to_string=True);continue
 timer,channel,state=pairs[0];target=timer['target'];block=target&~4095;headers=[]
 for lo,raw in regions(inf):
  needle=struct.pack('<Q',block);pos=raw.find(needle)
  while pos!=-1:
   if pos>=16 and pos+48<len(raw) and pos%8==0:
    kind=raw[pos+8];flags=raw[pos+9];size,descr=struct.unpack_from('<QQ',raw,pos+16)
    if kind==1 and not(flags&4) and size==32 and descr==24:headers.append(lo+pos-16)
   pos=raw.find(needle,pos+1)
 row['header_candidates']=headers
 if len(headers)!=1:
  row['selection_error']='expected unique normal 32-byte BDWGC header';gdb.execute('kill',to_string=True);continue
 header=headers[0];mark=header+64+((target-block)>>4)
 row.update({'timer':target,'channel':channel,'state':state,'mark_address':mark,'initial_mark':bytes(inf.read_memory(mark,1))[0]})
 bp.enabled=False
 wp=gdb.Breakpoint('*(unsigned char*)'+hex(mark),type=gdb.BP_WATCHPOINT,wp_class=gdb.WP_WRITE,internal=True)
 captures=[]
 for mark_event in range(1,9):
  last=None;tr=gdb.execute('continue',to_string=True)
  if isinstance(last,gdb.ExitedEvent):row['exit_while_watching']=last.exit_code;break
  value=bytes(inf.read_memory(mark,1))[0]
  capture={'event':type(last).__name__,'mark_value':value,'transcript':tr,'stack':gdb.execute('bt 32',to_string=True),'registers':gdb.execute('info registers',to_string=True)}
  captures.append(capture)
  if value:
   failure=out/('mark-source-%04d'%iteration);failure.mkdir()
   (failure/'mark-events.json').write_text(json.dumps(captures,indent=2))
   (failure/'maps.txt').write_text(mappings(inf))
   (failure/'threads.txt').write_text(gdb.execute('thread apply all bt 32',to_string=True))
   (failure/'registers.txt').write_text(gdb.execute('thread apply all info registers',to_string=True))
   hits=[]
   for lo,raw in regions(inf):
    for tgt in (target,channel):
     pos=raw.find(struct.pack('<Q',tgt))
     while pos!=-1:
      if pos%8==0:hits.append({'target':tgt,'address':lo+pos,'context_hex':raw[max(0,pos-64):pos+72].hex()})
      pos=raw.find(struct.pack('<Q',tgt),pos+1)
   (failure/'pointer-hits.json').write_text(json.dumps(hits,indent=2))
   gdb.execute('generate-core-file '+str(failure/'timer.core'),to_string=True)
   row['captured']=str(failure);found=True;break
 row['mark_events']=captures
 wp.delete()
 try:gdb.execute('kill',to_string=True)
 except gdb.error:pass
 print('MARK SOURCE',json.dumps(row),flush=True)
 if found:break
(out/'mark-source-result.json').write_text(json.dumps({'rows':rows,'found':found,'policy':'Additional mark-source instrumentation after repeated noncollection; no inferior calls; does not itself establish an unmodified semantic failure or a fix.'},indent=2))
if not found:gdb.execute('quit 2')
