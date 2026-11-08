import json, os, pathlib, subprocess, time
from win32stacks import snapshot
root = pathlib.Path.cwd()
out = root / '.compat111'
out.mkdir(exist_ok=True)
goroot = pathlib.Path(subprocess.check_output(['go','env','GOROOT'],text=True).strip())
llgo = os.environ['LLGO_TEST_LLGO']
results = []
def run(name, args, timeout=180):
    print('BEGIN',name,flush=True)
    start=time.monotonic()
    with (out/(name+'.stdout')).open('wb') as stdout, (out/(name+'.stderr')).open('wb') as stderr:
        p=subprocess.Popen([str(a) for a in args],stdout=stdout,stderr=stderr)
        sampled=False; next_progress=15; elapsed=0
        while p.poll() is None:
            elapsed=time.monotonic()-start
            if elapsed>=next_progress:
                print('PROGRESS',name,'pid',p.pid,round(elapsed,1),flush=True)
                next_progress+=15
            if elapsed>=45 and not sampled:
                sampled=True
                try:
                    data=snapshot(p.pid)
                    (out/(name+'-stacks.json')).write_text(json.dumps(data,indent=2))
                    print('STACKS',name,len(data.get('threads',[])),flush=True)
                except Exception as exc:
                    print('STACKS ERROR',repr(exc),flush=True)
            if elapsed>=timeout:
                subprocess.run(['taskkill','/pid',str(p.pid),'/t','/f'],capture_output=True,timeout=20)
                break
            time.sleep(1)
        try:
            p.wait(timeout=20)
        except subprocess.TimeoutExpired:
            p.kill()
        row=dict(name=name,seconds=time.monotonic()-start,code=p.poll())
        if elapsed>=timeout:
            row['timeout']=True
    results.append(row)
    (out/'results.json').write_text(json.dumps(results,indent=2))
    print('END',json.dumps(row),flush=True)
    for suffix in ('stdout','stderr'):
        data=(out/(name+'.'+suffix)).read_text(errors='replace')
        if data: print(name,suffix,data[-3000:],flush=True)
    return row
# Check the reported executable first; do not let another fixture hide its result.
source=(goroot/'test/fixedbugs/issue39541.go').read_text()
binary=out/'issue39541.exe'
if run('issue39541-build',[llgo,'build','-o',binary,goroot/'test/fixedbugs/issue39541.go'],240).get('code')==0:
    for n in range(3): run('issue39541-run-'+str(n),[binary],150)
progress=out/'issue39541-progress.go'
progress.write_text(source.replace('\t\t<-c\n','\t\t<-c; println("completed", i+1)\n'))
binary=out/'issue39541-progress.exe'
if run('progress-build',[llgo,'build','-o',binary,progress],240).get('code')==0:
    run('progress-run',[binary],150)
# Replay the compiler-crash and finalizer cases independently.
generator=out/'generator.exe'
if run('generator-build',['go','build','-o',generator,goroot/'test/convinline.go'],120).get('code')==0:
    run('generator-run',[generator],30)
    generated=out/'convinline.go'
    generated.write_bytes((out/'generator-run.stdout').read_bytes())
    binary=out/'convinline.exe'
    if run('convinline-build',[llgo,'build','-o',binary,generated],240).get('code')==0:
        run('convinline-run',[binary],30)
binary=out/'issue30476.exe'
if run('issue30476-build',[llgo,'build','-o',binary,goroot/'test/fixedbugs/issue30476.go'],120).get('code')==0:
    run('issue30476-run',[binary],30)
print(json.dumps(results,indent=2),flush=True)
if any(r.get('code')!=0 or r.get('timeout') for r in results): raise SystemExit(1)
