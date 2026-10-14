#!/usr/bin/env python3
"""Read-only, fenced transfer of a legacy numeric store into the Pulp telemetry owner."""
import json,os,sqlite3,sys
from pathlib import Path
from collector import bridge_event
from providers import configure
from pulp_backend import owner_config

def main():
    with open(os.environ.get('TELEMETRY_CONFIG','/config/telemetry.json')) as source:
        config=configure(json.load(source))
    bridge_event('bananapulse.telemetry.configure.v1',owner_config(config))
    path=Path(sys.argv[1]).resolve()
    db=sqlite3.connect(path.as_uri()+'?mode=ro',uri=True)
    try:
        # Cut over only after the old owner has finished submitting its durable observations.
        pending=db.execute('SELECT COUNT(*) FROM delivery_queue').fetchone()[0]
        if pending:raise RuntimeError('Drain legacy pending observations before migration')
        poll=db.execute('SELECT attempted,ok FROM poll_state WHERE id=1').fetchone()
        state=db.execute('SELECT state FROM policy_state WHERE id=1').fetchone()
        query=db.execute('SELECT at,metrics FROM samples ORDER BY at')
        total=0
        while True:
            rows=query.fetchmany(50)
            if not rows:break
            bridge_event('bananapulse.telemetry.import.v1',{'version':'telemetry.v1','moduleId':config['id'],
                'samples':[{'observedAt':at,'metrics':json.loads(metrics)} for at,metrics in rows]})
            total+=len(rows)
        bridge_event('bananapulse.telemetry.import.v1',{'version':'telemetry.v1','moduleId':config['id'],
            'samples':[],'finish':True,'attempted':poll[0] if poll else 1,'ok':bool(poll and poll[1]),
            'state':json.loads(state[0]) if state else {}})
        print(json.dumps({'owner':'pulp','importedSamples':total,'legacyStore':'retained-read-only'}))
    finally:db.close()
if __name__=='__main__':main()
