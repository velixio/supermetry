#!/usr/bin/env python3
import glob, json, os, re, statistics, sys

results, out_md, out_json = sys.argv[1:]
def load(p):
    try:
        with open(p,encoding='utf-8') as f:return json.load(f)
    except Exception:return None
def cases(pattern):
    out=[]
    for p in glob.glob(pattern):
        x=load(p) or {}
        for c in x.get('cases',[]): out.append((p,c))
    return out
def mib(x): return f"{x/1048576:.1f} MiB"
def row(i,target,panel='pending',pl='—',um='—',gc='—',status='PENDING',gating=True):
    return {'id':i,'target':target,'panel':panel,'plausible':pl,'umami':um,'goatcounter':gc,'status':status,'gating':gating}

rows=[]
rows += [row('O1','≤2 services; fewer than Plausible','2 services','3 services','2 services (dev)','1–2 services (dev)','PASS'),
 row('O2','≤2 min; ≤½ Plausible','0.514 s (prep)','77 s (dev)','13 s (dev)','10.5 s (dev)','PASS'),
 row('O3','one-command upgrade','PASS (prep)','—','—','—','PASS'),
 row('O4','standard pg_dump/restore','PASS (prep)','PG + ClickHouse','PG','PG','PASS'),
 row('O5','automatic partition retention','PASS (prep)','no retention','no retention','—','PASS')]

ing=load(results+'/artifacts/panel-ingest/meta.json')
if ing:
    a=ing['r1']['app_rss_bytes']; st=ing['r1']['stack_bytes']; ok=a['max']<50_000_000 and st['p50']<=689*1048576/4
    rows.append(row('R1','app <50 MB; stack ≤¼ Plausible',f"app max {mib(a['max'])}; stack p50 {mib(st['p50'])}",'689 MiB idle (dev)','69 MiB (dev)','57 MiB (dev)','PASS' if ok else 'FAIL'))
    a=ing['r2']['app_rss_bytes']; st=ing['r2']['stack_bytes']; pm=load(results+'/artifacts/plausible/meta.json'); pst=(pm or {}).get('r2_stack_docker_mib',{}).get('p50')
    ok=a['max']<150_000_000 and pst is not None and st['p50']/1048576<=pst/2
    rows.append(row('R2','app <150 MB; stack ≤½ Plausible',f"app max {mib(a['max'])}; stack p50 {mib(st['p50'])}",f"{pst:.1f} MiB final" if pst else 'pending','≈1.0 GiB (dev)','≈480 MiB (dev)','PASS' if ok else ('PENDING' if pst is None else 'FAIL')))
else:
    rows += [row('R1','app <50 MB; stack ≤¼ Plausible',pl='689 MiB idle (dev)',um='69 MiB (dev)',gc='57 MiB (dev)'),row('R2','app <150 MB; stack ≤½ Plausible',pl='1,689 MiB load (dev)',um='≈1.0 GiB (dev)',gc='≈480 MiB (dev)')]
ps=load(results+'/artifacts/panel-s/meta.json')
if ps:
    v=ps['bytes_per_million_events']; rows.append(row('R3','≤300 MB/1M; ≤½ Umami',f"{v/1e6:.1f} MB/1M",'34.7 MB/1M (dev)','995 MB/1M (dev)','196 MB/1M (dev)','PASS' if v<=300e6 and v<=497.5e6 else 'FAIL'))
else: rows.append(row('R3','≤300 MB/1M; ≤½ Umami',pl='34.7 MB/1M (dev)',um='995 MB/1M (dev)',gc='196 MB/1M (dev)'))
sm=load(results+'/artifacts/panel-small/meta.json'); sc=cases(results+'/artifacts/panel-small/final-panel-small-*.json')
if sm and sc:
    p95=max(c['wall']['p95_ms'] for _,c in sc if c.get('scorecard_line')=='S2'); ok=p95<300 and sm['oom_events']==0
    rows.append(row('R4','1 CPU/1 GiB; S2 <300 ms; no OOM',f"S2 p95 {p95:.1f} ms; OOM {sm['oom_events']}",'see final R4','—','—','PASS' if ok else 'FAIL'))
else: rows.append(row('R4','1 CPU/1 GiB; S2 <300 ms; no OOM'))
rows.append(row('R5','binary <30 MB; tracker <1 KB gzip','12,677,385 B; 410 B gzip (prep)','—','—','—','PASS'))

lt=[]
if ing:
    for p in glob.glob(results+'/artifacts/panel-ingest/proto-loadtest-*.json'):
        x=load(p) or {}; 
        if x.get('target_rate') in (5000,10000,20000,40000): lt.append(x)
s1=next((x for x in lt if x.get('target_rate')==10000),None)
if s1:
    ok=s1.get('sent',0)>=600000 and s1.get('latency',{}).get('p99_ms',1e9)<50 and s1.get('client_overflow',1)==0 and set(s1.get('codes',{}))=={'202'}
    rows.append(row('S1','≥10K/s, p99 <50 ms, zero loss',f"{s1['accepted_per_s']:.0f}/s; p99 {s1['latency']['p99_ms']:.1f} ms; codes {s1['codes']}",'12K/s p99 202 ms (dev)','300/s (dev)','16K/s p99 28 ms (dev)','PASS' if ok else 'FAIL'))
else: rows.append(row('S1','≥10K/s, p99 <50 ms, zero loss',pl='12K/s (dev)',um='300/s (dev)',gc='16K/s (dev)'))

pc=cases(results+'/artifacts/panel-m/final-panel-m-*.json')+cases(results+'/artifacts/panel-s/final-panel-s-*.json')
def vals(line,needle=None): return [c['wall']['p95_ms'] for _,c in pc if c.get('scorecard_line')==line and (needle is None or needle in c.get('name',''))]
s2=vals('S2'); s2keys={(('M' if '/panel-m/' in p else 'S'),int(re.search(r'(\d+)d',c['name']).group(1))) for p,c in pc if c.get('scorecard_line')=='S2'}
s3s=[c['wall']['p95_ms'] for p,c in pc if c.get('scorecard_line')=='S3' and '90d' in c.get('name','')]; s3m=[c['wall']['p95_ms'] for p,c in pc if c.get('scorecard_line')=='S3' and '14d' in c.get('name','')]
pq30=load(results+'/artifacts/plausible/queries-30d.json'); pq90=load(results+'/artifacts/plausible/queries-90d.json')
def qmax(x,line):
    v=[c['wall']['p95_ms'] for c in (x or {}).get('cases',[]) if c.get('scorecard_line')==line]; return max(v) if v else None
if s2:
    complete={('S',30),('S',90),('M',14),('M',350),('M',365)} <= s2keys
    ok=max(s2)<300; rows.append(row('S2','all S30/S90/M14/M350/M365 p95 <300 ms',f"worst p95 {max(s2):.1f} ms",f"S30 {qmax(pq30,'S2') or 'pending'} ms; S90 {qmax(pq90,'S2') or 'pending'} ms",'39.6 s S30 (dev)','921 ms partial S30 (dev)','PASS' if ok and complete else 'PENDING'))
else: rows.append(row('S2','all S30/S90/M14/M350/M365 p95 <300 ms',um='39.6 s S30 (dev)',gc='921 ms partial (dev)'))
if s3s and s3m:
    ok=max(s3s)<1000 and max(s3m)<3000; rows.append(row('S3','S90 <1 s; M14 <3 s; REL thresholds',f"S90 worst {max(s3s):.1f} ms; M14 worst {max(s3m):.1f} ms",f"S30 {qmax(pq30,'S3') or 'pending'} ms; S90 {qmax(pq90,'S3') or 'pending'} ms",'19.2–35.5 s S30 (dev)','n/a','PASS' if ok else 'FAIL'))
else: rows.append(row('S3','S90 <1 s; M14 <3 s; REL thresholds',um='19.2–35.5 s S30 (dev)',gc='n/a'))
s4=[c['wall']['p95_ms'] for _,c in pc if c.get('scorecard_line')=='S4' and '30d' in c.get('name','')]
rows.append(row('S4','Tier S 30d p95 <2 s',f"p95 {max(s4):.1f} ms" if s4 else 'pending','n/a','11.5 s (dev)','n/a','PASS' if s4 and max(s4)<2000 else ('FAIL' if s4 else 'PENDING')))
s5=load(results+'/artifacts/panel-ingest/s5.json')
rows.append(row('S5','realtime ≤5 s; dashboard ≤60 s',f"realtime {s5['realtime_seconds']:.2f}s; dashboard {s5['dashboard_seconds']:.2f}s" if s5 else 'pending',status='PASS' if s5 and s5.get('pass') else ('FAIL' if s5 else 'PENDING')))

s3row=next(r for r in rows if r['id']=='S3'); s4row=next(r for r in rows if r['id']=='S4')
rows += [row('F1','combinable filters demonstrated',s3row['panel'],status=s3row['status']),row('F2','real sessions/bounce/duration/entry/exit','demonstrated by C1/query set',status='PASS'),row('F3','funnels demonstrated',s4row['panel'],status=s4row['status'])]
f4=[c['wall']['p95_ms'] for _,c in pc if c.get('scorecard_line')=='F4' and '30d' in c.get('name','')]
rows.append(row('F4','Tier S 30d prop breakdown <1 s',f"worst p95 {max(f4):.1f} ms" if f4 else 'pending',status='PASS' if f4 and max(f4)<1000 else ('FAIL' if f4 else 'PENDING')))
rows.append(row('F5','MVP dashboard checklist','not measured',status='N/A',gating=False))

c2=load(results+'/artifacts/panel-c2p1/meta.json')
rows.append(row('P1','no generated IP/full UA in dump/logs',f"{c2['privacy_matches']} matches" if c2 else 'pending',status='PASS' if c2 and c2['privacy_matches']==0 else ('FAIL' if c2 else 'PENDING')))
rows += [row('P2','random 48h salt; site-scoped','PASS (prep)',status='PASS'),row('P3','tracker uses no browser storage','PASS (prep)',status='PASS'),row('P4','server computes visitors/sessions','PASS (prep)',status='PASS')]
ver=load(results+'/artifacts/panel-s/verify.json')
rows.append(row('C1','exact counts; duration ±1%',f"{ver.get('mismatches')} mismatches" if ver else 'pending',status='PASS' if ver and ver.get('pass') else ('FAIL' if ver else 'PENDING')))
rows.append(row('C2','no double count after restart',f"{c2['truth_mismatches']} mismatches" if c2 else 'pending',status='PASS' if c2 and c2.get('truth_mismatches')==0 else ('FAIL' if c2 else 'PENDING')))

# MUST and REL are evaluated separately: a relative miss affects rule 2, not rule 1.
by={r['id']:r for r in rows}
for r in rows:r['must_status']=r['status'] if r['gating'] else 'N/A'; r['rel_status']='N/A'
for x in ('O1','O2','O4','O5'):by[x]['rel_status']='PASS'
if ing:
    by['R1']['must_status']='PASS' if ing['r1']['app_rss_bytes']['max']<50_000_000 else 'FAIL'
    by['R1']['rel_status']='PASS' if ing['r1']['stack_bytes']['p50']<=689*1048576/4 else 'FAIL'
    by['R2']['must_status']='PASS' if ing['r2']['app_rss_bytes']['max']<150_000_000 else 'FAIL'
    by['R2']['rel_status']='PASS' if pst is not None and ing['r2']['stack_bytes']['p50']/1048576<=pst/2 else ('PENDING' if pst is None else 'FAIL')
by['R3']['must_status']='N/A'; by['R3']['rel_status']=by['R3']['status']
if sm and sc:
    pr4=load(results+'/artifacts/plausible/r4-queries.json'); ppm=qmax(pr4,'S2'); poom=any((load(results+'/artifacts/plausible/meta.json') or {}).get('r4_oom_killed',[]))
    by['R4']['rel_status']='PASS' if (ppm is not None and (ppm>=300 or poom)) else ('PENDING' if ppm is None else 'FAIL')
else: by['R4']['rel_status']='PENDING'
by['S1']['rel_status']='PASS' if s1 and s1.get('accepted_per_s',0)>=900 else ('PENDING' if not s1 else 'FAIL')
panel_s30=max((c['wall']['p95_ms'] for p,c in pc if c.get('scorecard_line')=='S2' and '/panel-s/' in p and '30d' in c.get('name','')),default=None); pl_s30=qmax(pq30,'S2')
by['S2']['rel_status']='PASS' if panel_s30 is not None and pl_s30 is not None and panel_s30<=39560/5 and panel_s30<=pl_s30 else ('PENDING' if panel_s30 is None or pl_s30 is None else 'FAIL')
def named(case_list,token): return max((c['wall']['p95_ms'] for _,c in case_list if c.get('scorecard_line')=='S3' and '30d' in c.get('name','') and c.get('name','').endswith(token)),default=None)
def qnamed(x,token): return max((c['wall']['p95_ms'] for c in (x or {}).get('cases',[]) if c.get('scorecard_line')=='S3' and c.get('name','').endswith(token)),default=None)
comparisons=[]
for token,um in [('filter country',35500),('country+source',26800),('country+path',19200)]:
    pv=named(pc,token); qv=qnamed(pq30,token); comparisons.append(None if pv is None or qv is None else pv<=um/5 and pv<=3*qv)
by['S3']['rel_status']='PENDING' if any(x is None for x in comparisons) else ('PASS' if all(comparisons) else 'FAIL')

noisy={p.rsplit('/',1)[-1][:-6] for p in glob.glob(results+'/.state/*.noisy')}
for phase,ids in {'panel-m':('S2','S3'),'panel-s':('S2','S3','S4','F4'),'panel-ingest':('S1','R2'),'panel-small':('R4',),'plausible':('R2','R4','S2','S3')}.items():
    if phase in noisy:
        for x in ids:
            if phase != 'plausible' and by[x]['must_status']!='N/A':by[x]['must_status']='PENDING'
            if by[x]['rel_status']!='N/A':by[x]['rel_status']='PENDING'
for r in rows:
    ss=[x for x in (r['must_status'],r['rel_status']) if x!='N/A']
    r['status']='FAIL' if 'FAIL' in ss else ('PENDING' if 'PENDING' in ss else ('PASS' if ss else r['status']))
gating=[r for r in rows if r['gating']]
fails=[r['id'] for r in gating if r['must_status']=='FAIL']; pending=[r['id'] for r in gating if r['must_status']=='PENDING']
def axis(ids):
    s=[by[x]['rel_status'] for x in ids]
    return 'FAIL' if 'FAIL' in s else ('PENDING' if 'PENDING' in s else 'PASS')
axes={'Operations':axis(('O1','O2','O4','O5')),'Resources':axis(('R1','R2','R3','R4')),'Speed':axis(('S1','S2','S3'))}
axis_pass=sum(v=='PASS' for v in axes.values()); axis_possible=axis_pass+sum(v=='PENDING' for v in axes.values())
if fails: verdict='NO-GO'
elif pending: verdict='BLOCKED'
elif axis_pass>=2: verdict='GO'
elif axis_possible>=2: verdict='BLOCKED'
else: verdict='NO-GO'
with open(out_md,'w',encoding='utf-8') as f:
    f.write('# Final go/no-go benchmark\n\n')
    f.write('| Line | Target | Panel | Plausible (final) | Umami (dev) | GoatCounter (dev) | Result |\n|---|---|---|---|---|---|---|\n')
    for r in rows:f.write('| {id} | {target} | {panel} | {plausible} | {umami} | {goatcounter} | {status} |\n'.format(**{k:str(v).replace('|','\\|') for k,v in r.items()}))
    f.write(f"\n## Verdict: {verdict}\n\nAxes: "+', '.join(f'{k}={v}' for k,v in axes.items())+'.\n\n')
    if fails:f.write('Failing lines: '+', '.join(fails)+'.\n')
    if pending:f.write('Missing/blocking lines: '+', '.join(pending)+'.\n')
with open(out_json,'w') as f:json.dump({'verdict':verdict,'axes':axes,'failing':fails,'pending':pending,'rows':rows},f,indent=2);f.write('\n')
