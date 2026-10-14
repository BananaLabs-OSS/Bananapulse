"""Native transport edge for the Pulp cells. No metric database or alert state lives here."""
import hmac,http.server,json,os,threading,time
from providers import collect

def owner_config(config):
    policy=config.get('policy',{})
    fields=['enabled','metricId','readyMetric','direction','below','above','recoverAbove','recoverBelow',
            'badForSeconds','recoveryForSeconds','sourceId']
    return {'version':'telemetry.v1','id':config['id'],'componentId':config['componentId'],
            'ttlSeconds':config.get('ttlSeconds',120),'retentionDays':config.get('retentionDays',7),
            'policy':{key:policy[key] for key in fields if key in policy}}

def report(config,at,payload,ok,publish):
    return {'version':'game-server.v1','adapter':config['adapter'],'moduleId':config['id'],
            'componentId':config['componentId'],'observedAt':at,'ok':ok,'publish':publish,
            'payload':json.dumps(payload) if ok else '', 'metrics':config['metrics']}

def main(config,bridge,initialize,can_publish):
    # Bootstrap never creates a second policy store; the Pulp owner resumes its durable state.
    bridge('bananapulse.telemetry.configure.v1',owner_config(config))
    initialize(config)
    token=os.environ['PULP_BRIDGE_TOKEN']
    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            if not hmac.compare_digest(self.headers.get('Authorization',''),'Bearer '+token):
                self.send_response(401);self.end_headers();return
            kind={'/snapshot':'snapshot','/history':'history'}.get(self.path)
            if not kind:self.send_response(404);self.end_headers();return
            try:
                now=int(time.time());value=bridge('bananapulse.telemetry.'+kind+'.v1',
                    {'version':'telemetry.v1','moduleId':config['id'],'now':now,'since':now-86400})
            except Exception:
                self.send_response(503);self.end_headers();return
            self.send_response(200);self.send_header('Content-Type','application/json');self.send_header('Cache-Control','no-store');self.end_headers()
            self.wfile.write(json.dumps(value).encode())
        def log_message(self,*args):pass
    server=http.server.ThreadingHTTPServer(('127.0.0.1',int(config.get('port',8790))),Handler)
    threading.Thread(target=server.serve_forever,daemon=True).start()
    while True:
        started=time.monotonic();at=int(time.time())
        try:payload=collect(config);ok=True
        except Exception:payload=None;ok=False
        try:publish=ok and can_publish(config)
        except Exception:publish=False
        try:
            bridge('bananapulse.telemetry.poll.v1',report(config,at,payload,ok,publish))
        except Exception:
            # Bad/missing provider fields are a failed sample, never a fabricated recovery.
            try:bridge('bananapulse.telemetry.poll.v1',report(config,at,None,False,False))
            except Exception:pass
            status='unavailable'
        else:status='ok' if ok else 'unavailable'
        print(json.dumps({'module':config['id'],'sample':status,'owner':'pulp','observedAt':at}),flush=True)
        time.sleep(max(1,config.get('intervalSeconds',30)-(time.monotonic()-started)))
