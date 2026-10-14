#!/usr/bin/env python3
"""Optional JSON telemetry collector. Integration/policy are configuration, not core state."""
import contextlib, base64, email.utils, hashlib, hmac, http.server, json, math, os, secrets, sqlite3, threading, time, urllib.request
from urllib.parse import urlsplit

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args): return None

def fetch_payload(config):
    url = config['url']
    parsed = urlsplit(url)
    if parsed.scheme != 'https' or parsed.username or parsed.password or parsed.fragment:
        raise ValueError('Collector requires a credential-free HTTPS URL')
    headers = {'Accept': 'application/json'}
    if config.get('auth') == 'vrage-hmac-sha1':
        key = os.environ[config.get('keyEnv', 'VRAGE_KEY')]
        path = parsed.path + ('?' + parsed.query if parsed.query else '')
        nonce = str(secrets.randbelow(2**31))
        date = email.utils.formatdate(usegmt=True)
        digest = hmac.new(key.encode(), f'{path}\r\n{nonce}\r\n{date}\r\n'.encode(), hashlib.sha1).digest()
        headers.update(Date=date, Authorization=nonce + ':' + base64.b64encode(digest).decode())
    elif config.get('auth', 'none') != 'none':
        raise ValueError('Unsupported collector authentication')
    request = urllib.request.Request(url, headers=headers, method='GET')
    with urllib.request.build_opener(NoRedirect).open(request, timeout=10) as response:
        body = response.read(65537)
        if len(body) > 65536: raise ValueError('Telemetry response exceeds limit')
        return json.loads(body)

def extract(payload, definitions):
    metrics = []
    for definition in definitions:
        value = payload
        for part in definition['path'].split('.'):
            if not isinstance(value, dict) or part not in value: raise ValueError('Missing metric')
            value = value[part]
        if isinstance(value, bool):
            if definition.get('unit') != 'boolean': raise ValueError('Unexpected boolean metric')
            value = int(value)
        if not isinstance(value, (int, float)) or not math.isfinite(value): raise ValueError('Invalid numeric metric')
        metrics.append({'id': definition['id'], 'label': definition['label'], 'unit': definition['unit'], 'value': value})
    return metrics

class Store:
    def __init__(self, path, config):
        self.path, self.config = path, config
        self.lock = threading.Lock()
        with self.connect() as db:
            db.execute('CREATE TABLE IF NOT EXISTS samples (at INTEGER PRIMARY KEY, metrics TEXT NOT NULL)')
            db.execute('CREATE TABLE IF NOT EXISTS policy_state (id INTEGER PRIMARY KEY CHECK(id=1), state TEXT NOT NULL)')
            db.execute('CREATE TABLE IF NOT EXISTS delivery_queue (id TEXT PRIMARY KEY, payload TEXT NOT NULL, created INTEGER NOT NULL)')
            db.execute('CREATE TABLE IF NOT EXISTS poll_state (id INTEGER PRIMARY KEY CHECK(id=1), attempted INTEGER, ok INTEGER)')
        os.chmod(path, 0o600)
    @contextlib.contextmanager
    def connect(self):
        db = sqlite3.connect(self.path, timeout=5)
        try:
            with db: yield db
        finally: db.close()
    def record(self, at, metrics=None):
        with self.lock, self.connect() as db:
            db.execute('INSERT OR REPLACE INTO poll_state VALUES(1,?,?)', (at, int(metrics is not None)))
            if metrics is not None:
                db.execute('INSERT OR REPLACE INTO samples VALUES(?,?)', (at,json.dumps(metrics)))
                policy = self.config.get('policy')
                if policy and policy.get('enabled'):
                    row = db.execute('SELECT state FROM policy_state WHERE id=1').fetchone()
                    state, signal = advance_policy(json.loads(row[0]) if row else {}, metrics, at, policy, self.config.get('ttlSeconds',120))
                    db.execute('INSERT OR REPLACE INTO policy_state VALUES(1,?)', (json.dumps(state),))
                    if signal:
                        command = {'version':'monitor.v1','id':secrets.token_hex(16),'kind':'ingest_observation','at_unix':at,
                            'ingest':{'observation_id':secrets.token_hex(16),'source_id':policy['sourceId'],'component_id':self.config['componentId'],
                                'signal':signal,'detail':json.dumps({'module':self.config['id'],'metrics':metrics}),
                                'observed_at_unix':at,'expires_at_unix':at+self.config.get('ttlSeconds',120)}}
                        db.execute('INSERT INTO delivery_queue VALUES(?,?,?)',(command['id'],json.dumps(command),at))
            if metrics is None:
                row=db.execute('SELECT state FROM policy_state WHERE id=1').fetchone()
                if row:
                    state=json.loads(row[0]);state.pop('badSince',None);state.pop('goodSince',None)
                    db.execute('UPDATE policy_state SET state=? WHERE id=1',(json.dumps(state),))
            db.execute('DELETE FROM delivery_queue WHERE created < ?', (at-self.config.get('ttlSeconds',120),))
            db.execute('DELETE FROM samples WHERE at < ?', (at-int(self.config.get('retentionDays',7))*86400,))
    def snapshot(self, now):
        with self.lock, self.connect() as db:
            row = db.execute('SELECT at,metrics FROM samples ORDER BY at DESC LIMIT 1').fetchone()
            state = db.execute('SELECT attempted,ok FROM poll_state WHERE id=1').fetchone()
        return {'version':'telemetry.v1', 'moduleId':self.config['id'], 'componentId':self.config['componentId'],
                'observedAt':row[0] if row else None, 'expiresAt':row[0]+self.config.get('ttlSeconds',120) if row else None,
                'stale':not row or row[0]+self.config.get('ttlSeconds',120)<=now,
                'lastPollOk':bool(state and state[1]), 'lastAttemptAt':state[0] if state else None,
                'metrics':json.loads(row[1]) if row else []}
    def history(self, since, limit=2000):
        with self.lock, self.connect() as db:
            rows = db.execute('SELECT at,metrics FROM samples WHERE at>=? ORDER BY at DESC LIMIT ?', (since,min(limit,2000))).fetchall()
        return [{'observedAt':at,'metrics':json.loads(metrics)} for at,metrics in reversed(rows)]

def advance_policy(state, metrics, now, policy, ttl):
    state = dict(state)
    if now - state.get('lastSampleAt',now) > ttl:
        state.pop('badSince',None); state.pop('goodSince',None)
    state['lastSampleAt'] = now
    values = {m['id']:m['value'] for m in metrics}
    if policy.get('readyMetric') and values.get(policy['readyMetric']) != 1:
        state.pop('badSince',None);state.pop('goodSince',None)
        return state,None  # Loading or missing readiness is not evidence of recovered performance.
    value = values[policy['metricId']]
    if value < policy['below']:
        state.pop('goodSince',None)
        state.setdefault('badSince',now)
        if now-state['badSince'] >= policy['badForSeconds']: state['degraded']=True
    elif value >= policy['recoverAbove']:
        state.pop('badSince',None)
        state.setdefault('goodSince',now)
        if now-state['goodSince'] >= policy['recoveryForSeconds']: state['degraded']=False
    else:
        state.pop('badSince',None);state.pop('goodSince',None)
    return state,'degraded' if state.get('degraded') else 'ok'

def bridge_event(event, body):
    base=os.environ['PULP_BRIDGE_URL']
    request=urllib.request.Request(base+'/internal/v1/events/'+event,data=json.dumps(body).encode(),
        headers={'Content-Type':'application/json','X-Pulp-Bridge-Token':os.environ['PULP_BRIDGE_TOKEN']},method='POST')
    with urllib.request.urlopen(request,timeout=10) as response:
        return json.loads(response.read(65536))

def initialize_policy(config):
    policy=config.get('policy')
    if not policy or not policy.get('enabled'): return
    component=policy['component']
    # Config changes create a new receipt; retries retain the same command ID.
    digest=hashlib.sha256(json.dumps(component,sort_keys=True).encode()).hexdigest()[:16]
    bridge_event('bananapulse.monitor.admin.command.v1',{'version':'monitor.v1','id':config['id']+'-component-'+digest,
        'kind':'upsert_component','at_unix':int(time.time()),'component':component})
    source={'id':policy['sourceId'],'name':policy.get('sourceName','Telemetry performance rule'),'kind':'push',
        'trusted':True,'weight':1,'direct_targets':True,'default_ttl_seconds':config.get('ttlSeconds',120)}
    digest=hashlib.sha256(json.dumps(source,sort_keys=True).encode()).hexdigest()[:16]
    bridge_event('bananapulse.monitor.admin.command.v1',{'version':'monitor.v1','id':config['id']+'-source-'+digest,
        'kind':'upsert_source','at_unix':int(time.time()),'source':source})

def deliver(store):
    with store.lock, store.connect() as db:
        rows=db.execute('SELECT id,payload FROM delivery_queue ORDER BY rowid').fetchall()
    for ident,raw in rows:
        command=json.loads(raw)
        # Expired pending readings are not replayed as current health after downtime.
        if command['ingest']['expires_at_unix']>int(time.time()):
            bridge_event('bananapulse.monitor.ingest.authenticated.v1',command)
        with store.lock,store.connect() as db: db.execute('DELETE FROM delivery_queue WHERE id=?',(ident,))
        policy=store.config.get('policy',{})
        if command['ingest']['signal']=='degraded' and policy.get('alertTitle'):
            projection=bridge_event('bananapulse.monitor.query.v1',{'version':'monitor.v1','at_unix':int(time.time())})
            for incident in projection.get('incidents',[]):
                if incident.get('auto') and not incident.get('resolved_at_unix') and incident.get('affects')==[store.config['componentId']] and incident.get('title')!=policy['alertTitle']:
                    ident=hashlib.sha256((incident['id']+policy['alertTitle']).encode()).hexdigest()
                    bridge_event('bananapulse.monitor.admin.command.v1',{'version':'monitor.v1','id':ident,'kind':'edit_incident','at_unix':int(time.time()),
                        'incident_patch':{'id':incident['id'],'title':policy['alertTitle'],'summary':policy['alertSummary']}})


def handler(store, token):
    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            supplied=self.headers.get('Authorization','')
            if not token or not hmac.compare_digest(supplied,'Bearer '+token):
                self.send_response(401); self.end_headers(); return
            if self.path=='/snapshot': value=store.snapshot(int(time.time()))
            elif self.path=='/history': value={'version':'telemetry.v1','samples':store.history(int(time.time())-86400)}
            else: self.send_response(404); self.end_headers(); return
            self.send_response(200); self.send_header('Content-Type','application/json'); self.send_header('Cache-Control','no-store'); self.end_headers()
            self.wfile.write(json.dumps(value).encode())
        def log_message(self,*args): pass
    return Handler

def main():
    config=json.load(open(os.environ.get('TELEMETRY_CONFIG','/config/telemetry.json')))
    if len(config['metrics'])>32: raise ValueError('Too many metric definitions')
    token=os.environ['PULP_BRIDGE_TOKEN']
    store=Store(os.environ.get('TELEMETRY_DB','/data/telemetry.sqlite'),config)
    server=http.server.ThreadingHTTPServer(('127.0.0.1',int(config.get('port',8790))),handler(store,token))
    threading.Thread(target=server.serve_forever,daemon=True).start()
    initialized=False
    while True:
        started=time.monotonic();at=int(time.time())
        try:
            metrics=extract(fetch_payload(config),config['metrics'])
        except Exception:
            store.record(at)
            print(json.dumps({'module':config['id'],'sample':'unavailable','observedAt':at}),flush=True)
        else:
            store.record(at,metrics)
            print(json.dumps({'module':config['id'],'sample':'ok','observedAt':at}),flush=True)
            try:
                if not initialized: initialize_policy(config);initialized=True
                deliver(store)
            except Exception:
                # Delivery retries preserve IDs. Provider credentials and errors never enter logs.
                print(json.dumps({'module':config['id'],'statusDelivery':'unavailable'}),flush=True)
        time.sleep(max(1,config.get('intervalSeconds',30)-(time.monotonic()-started)))
if __name__=='__main__': main()
