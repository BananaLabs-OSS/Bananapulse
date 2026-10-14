"""Space Engineers VRage server-info adapter. Raw keys stay in the process environment."""
import base64,email.utils,hashlib,hmac,os,secrets
from providers import http_json

FIELDS=[('ready','Server ready','boolean','IsReady'),
        ('simulation_speed','Simulation speed','ratio','SimSpeed'),
        ('simulation_cpu_load','Simulation CPU load','reported','SimulationCpuLoad'),
        ('players','Players','count','Players'),('used_pcu','Used PCU','count','UsedPCU'),
        ('pirate_pcu','Pirate PCU','count','PirateUsedPCU')]
METRICS=[{'id':i,'label':label,'unit':unit,'path':'data.'+field} for i,label,unit,field in FIELDS]

def collect(config):
    parsed=http_json.validate_url(config['url'])
    key=os.environ[config.get('keyEnv','VRAGE_KEY')]
    path=parsed.path+('?' + parsed.query if parsed.query else '')
    nonce=str(secrets.randbelow(2**31));date=email.utils.formatdate(usegmt=True)
    digest=hmac.new(key.encode(),f'{path}\r\n{nonce}\r\n{date}\r\n'.encode(),hashlib.sha1).digest()
    return http_json.fetch(config,{'Date':date,'Authorization':nonce+':'+base64.b64encode(digest).decode()})
