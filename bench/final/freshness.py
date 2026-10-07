#!/usr/bin/env python3
import argparse,json,time,urllib.parse,urllib.request
from datetime import datetime
from zoneinfo import ZoneInfo
p=argparse.ArgumentParser();p.add_argument('--url',required=True);p.add_argument('--out',required=True);a=p.parse_args()
day=datetime.now(ZoneInfo('America/New_York')).date().isoformat()
def get(path):
 with urllib.request.urlopen(a.url+path,timeout=5) as r:return json.load(r)
q='/api/query/overview?'+urllib.parse.urlencode({'site':1,'from':day,'to':day,'mode':'hot'})
before=get(q)['result']['pageviews']; active_before=get('/api/realtime?site=1')['active_visitors_5m']; body=json.dumps({'n':'pageview','u':'https://site1.test/final-freshness','d':'site1.test'}).encode()
req=urllib.request.Request(a.url+'/api/event',body,method='POST',headers={'Content-Type':'text/plain','User-Agent':'Mozilla/5.0 FinalFreshness/1.0','X-Forwarded-For':'198.51.100.249'})
t=time.monotonic()
with urllib.request.urlopen(req,timeout=5) as r: assert r.status==202
rt=db=None
while time.monotonic()-t<60:
 if rt is None and get('/api/realtime?site=1')['active_visitors_5m']>active_before:rt=time.monotonic()-t
 if get(q)['result']['pageviews']>=before+1:db=time.monotonic()-t;break
 time.sleep(.25)
x={'realtime_seconds':rt,'dashboard_seconds':db,'pass':rt is not None and rt<=5 and db is not None and db<=60}
with open(a.out,'w') as f:json.dump(x,f,indent=2);f.write('\n')
if not x['pass']:raise SystemExit(1)
