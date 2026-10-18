from pathlib import Path
import json,os,subprocess,time,hashlib
out=Path('/tmp/timer-candidate'); image=out/'timer';expected=hashlib.sha256(image.read_bytes()).hexdigest();rows=[]
for procs,count in [('1',100),('2',1000)]:
 for i in range(1,count+1):
  log=out/('p%s-%04d.log'%(procs,i));start=time.monotonic()
  with log.open('xb') as f:r=subprocess.run([str(image)],env=os.environ|{'GOMAXPROCS':procs,'LLGO_TIMER_GC_CONTROL':''},stdout=f,stderr=subprocess.STDOUT,timeout=30)
  text=log.read_text();row={'p':procs,'iteration':i,'exit':r.returncode,'seconds':time.monotonic()-start,'log':log.name,'sha256':hashlib.sha256(log.read_bytes()).hexdigest()};rows.append(row)
  with (out/'runs.jsonl').open('a') as f:f.write(json.dumps(row)+'\n')
  assert r.returncode==0 and text.strip()=='timer/ticker collection and manager retirement ok',text
  if i%100==0:print('PASS',procs,i,flush=True)
controls=[]
for mode,message in [('keep-timer','unreachable future Timer/Ticker or channel remained strongly alive'),('extra-g','collected future timer retained parked manager until deadline')]:
 log=out/(mode+'.log');start=time.monotonic()
 with log.open('xb') as f:r=subprocess.run([str(image)],env=os.environ|{'GOMAXPROCS':'2','LLGO_TIMER_GC_CONTROL':mode},stdout=f,stderr=subprocess.STDOUT,timeout=30)
 text=log.read_text();row={'mode':mode,'exit':r.returncode,'seconds':time.monotonic()-start,'expected_message':message,'log':log.name,'sha256':hashlib.sha256(log.read_bytes()).hexdigest()};controls.append(row);print('CONTROL',json.dumps(row),flush=True)
 assert r.returncode!=0 and message in text and 'timer/ticker collection and manager retirement ok' not in text,text
assert hashlib.sha256(image.read_bytes()).hexdigest()==expected
(out/'result.json').write_text(json.dumps({'image_sha256':expected,'positive_runs':len(rows),'topologies':{'1':100,'2':1000},'positive_all_passed':True,'negative_controls':controls,'policy':'Same128 selected weak references, five-second assertion and manager-count requirement. The collector page-boundary false root is documented separately; no production GC or timer changes.'},indent=2)+'\n')
