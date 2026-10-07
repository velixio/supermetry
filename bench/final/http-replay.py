#!/usr/bin/env python3
import argparse, concurrent.futures, json, urllib.request

p=argparse.ArgumentParser(); p.add_argument('--url',required=True); p.add_argument('--input',required=True); p.add_argument('--from-line',type=int,default=1); p.add_argument('--to-line',type=int,default=0); p.add_argument('--workers',type=int,default=32); p.add_argument('--secrets'); p.add_argument('--out',required=True); a=p.parse_args()
rows=[]; secrets=set()
with open(a.input,encoding='utf-8') as f:
    for n,line in enumerate(f,1):
        x=json.loads(line); secrets.update((x['ip'],x['ua']))
        if n>=a.from_line and (not a.to_line or n<=a.to_line): rows.append(x)
if a.secrets:
    with open(a.secrets,'w',encoding='utf-8') as f:
        for x in sorted(secrets): f.write(x+'\n')
def send(x):
    body={'n':x['name'],'u':'https://'+x['host']+x['path'],'d':x['host'],'r':x['ref']}
    if x.get('props'): body['p']=x['props']
    req=urllib.request.Request(a.url+'/api/event',json.dumps(body).encode(),method='POST',headers={'Content-Type':'text/plain','User-Agent':x['ua'],'X-Forwarded-For':x['ip']})
    try:
        with urllib.request.urlopen(req,timeout=10) as r: return r.status
    except Exception as e: return getattr(e,'code',0)
with concurrent.futures.ThreadPoolExecutor(max_workers=a.workers) as ex: codes=list(ex.map(send,rows,chunksize=32))
out={'attempted':len(rows),'accepted':sum(x==202 for x in codes),'codes':{str(k):codes.count(k) for k in sorted(set(codes))}}
with open(a.out,'w') as f: json.dump(out,f,indent=2); f.write('\n')
if out['accepted'] != len(rows): raise SystemExit(1)

