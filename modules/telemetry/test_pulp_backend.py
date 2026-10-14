import sys,unittest
from pathlib import Path
sys.path.insert(0,str(Path(__file__).parent))
from pulp_backend import owner_config,report
class PulpEdgeTests(unittest.TestCase):
 def test_owner_configuration_excludes_provider_credentials_and_urls(self):
  config={'id':'vrage','componentId':'service','url':'https://private.example','keyEnv':'VRAGE_KEY',
    'policy':{'enabled':True,'metricId':'speed','sourceId':'source','component':{'name':'private'},'unexpected':'excluded'}}
  out=owner_config(config)
  self.assertNotIn('url',out);self.assertNotIn('keyEnv',out);self.assertNotIn('component',out['policy']);self.assertNotIn('unexpected',out['policy'])
 def test_failed_transport_report_carries_no_provider_data(self):
  config={'adapter':'game-server/space-engineers','id':'vrage','componentId':'service','metrics':[]}
  out=report(config,100,{'secret':'provider-data'},False,False)
  self.assertEqual(out['payload'],'');self.assertFalse(out['publish']);self.assertFalse(out['ok'])
