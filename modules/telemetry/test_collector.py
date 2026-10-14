import importlib.util,json,tempfile,unittest
from pathlib import Path
spec=importlib.util.spec_from_file_location('collector',Path(__file__).with_name('collector.py'));collector=importlib.util.module_from_spec(spec);spec.loader.exec_module(collector)
class CollectorTests(unittest.TestCase):
 def test_maps_allowlisted_metrics_and_rejects_missing_nan_and_wrong_types(self):
  fields=[{'id':'speed','label':'Speed','unit':'ratio','path':'data.SimSpeed'}]
  self.assertEqual(collector.extract({'data':{'SimSpeed':1.02,'secret':'excluded'}},fields)[0]['value'],1.02)
  for v in [True,float('nan'),'1.0']:
   with self.assertRaises(ValueError):collector.extract({'data':{'SimSpeed':v}},fields)
  with self.assertRaises(ValueError):collector.extract({'data':{}},fields)
 def test_failed_poll_preserves_history_but_expires_old_reading(self):
  with tempfile.TemporaryDirectory() as root:
   s=collector.Store(str(Path(root)/'samples.db'),{'id':'test','componentId':'service','ttlSeconds':120})
   s.record(100,[{'id':'speed','label':'Speed','unit':'ratio','value':1}]);s.record(150)
   self.assertFalse(s.snapshot(150)['lastPollOk']);self.assertFalse(s.snapshot(150)['stale']);self.assertTrue(s.snapshot(220)['stale'])
   self.assertEqual(len(s.history(0)),1)
   reopened=collector.Store(s.path,s.config);self.assertEqual(reopened.snapshot(150)['observedAt'],100)
if __name__=='__main__':unittest.main()

class PolicyTests(unittest.TestCase):
 def test_sustained_slowdown_hysteresis_and_recovery(self):
  p={'metricId':'speed','readyMetric':'ready','below':.8,'recoverAbove':.95,'badForSeconds':90,'recoveryForSeconds':120}
  state={}
  def step(at,speed,ready=1):
   nonlocal state
   state,signal=collector.advance_policy(state,[{'id':'speed','value':speed},{'id':'ready','value':ready}],at,p,120)
   return signal
  self.assertEqual(step(100,.7),'ok');self.assertEqual(step(160,.7),'ok');self.assertEqual(step(190,.7),'degraded')
  self.assertEqual(step(220,.9),'degraded');self.assertEqual(step(250,1),'degraded');self.assertEqual(step(310,1,0),None)
  self.assertEqual(step(340,1),'degraded');self.assertEqual(step(430,1),'degraded');self.assertEqual(step(460,1),'ok')
 def test_collection_gap_resets_sustained_breach_timer(self):
  p={'metricId':'speed','below':.8,'recoverAbove':.95,'badForSeconds':90,'recoveryForSeconds':120}
  state,_=collector.advance_policy({},[{'id':'speed','value':.7}],100,p,120)
  state,signal=collector.advance_policy(state,[{'id':'speed','value':.7}],400,p,120)
  self.assertEqual(signal,'ok');self.assertEqual(state['badSince'],400)

class DurablePolicyTests(unittest.TestCase):
 def test_failed_poll_resets_dwell_and_delivery_keeps_stable_receipts(self):
  policy={'enabled':True,'metricId':'speed','below':.8,'recoverAbove':.95,'badForSeconds':90,'recoveryForSeconds':120,'sourceId':'source'}
  with tempfile.TemporaryDirectory() as root:
   s=collector.Store(str(Path(root)/'samples.db'),{'id':'test','componentId':'service','policy':policy})
   s.record(100,[{'id':'speed','value':.7}]);s.record(130)
   reopened=collector.Store(s.path,s.config);reopened.record(160,[{'id':'speed','value':.7}])
   with reopened.connect() as db:
    state=json.loads(db.execute('SELECT state FROM policy_state').fetchone()[0]);rows=db.execute('SELECT id,payload FROM delivery_queue').fetchall()
   self.assertEqual(state['badSince'],160);self.assertFalse(state.get('degraded',False))
   self.assertEqual(len(rows),1)
   self.assertTrue(all(ident==json.loads(raw)['id'] for ident,raw in rows))

class LatencyPolicyTests(unittest.TestCase):
 def test_latency_requires_sustained_breach_and_clear_recovery(self):
  p={'metricId':'latency','direction':'above','above':300,'recoverBelow':150,'badForSeconds':120,'recoveryForSeconds':120}
  state={}
  def step(at,value):
   nonlocal state
   state,signal=collector.advance_policy(state,[{'id':'latency','value':value}],at,p,120)
   return signal
  self.assertEqual(step(100,301),'ok');self.assertEqual(step(160,500),'ok');self.assertEqual(step(220,500),'degraded')
  self.assertEqual(step(250,200),'degraded');self.assertEqual(step(280,149),'degraded');self.assertEqual(step(340,149),'degraded');self.assertEqual(step(400,149),'ok')

class AvailabilityGateTests(unittest.TestCase):
 def test_unavailable_game_keeps_private_metrics_without_alert_observation(self):
  p={'enabled':True,'metricId':'speed','below':.8,'recoverAbove':.95,'badForSeconds':0,'recoveryForSeconds':0,'sourceId':'source'}
  with tempfile.TemporaryDirectory() as root:
   s=collector.Store(str(Path(root)/'samples.db'),{'id':'test','componentId':'service','policy':p})
   s.record(100,[{'id':'speed','value':.7}],publish=False)
   with s.connect() as db:self.assertEqual(db.execute('select count(*) from delivery_queue').fetchone()[0],0)
   self.assertEqual(s.snapshot(110)['metrics'][0]['value'],.7)

class PendingGateTests(unittest.TestCase):
 def test_game_failure_discards_pending_performance_claims(self):
  p={'enabled':True,'metricId':'speed','below':.8,'recoverAbove':.95,'badForSeconds':0,'recoveryForSeconds':0,'sourceId':'source'}
  with tempfile.TemporaryDirectory() as root:
   s=collector.Store(str(Path(root)/'samples.db'),{'id':'test','componentId':'service','policy':p})
   s.record(100,[{'id':'speed','value':.7}]);s.record(130,[{'id':'speed','value':.7}],publish=False)
   with s.connect() as db:self.assertEqual(db.execute('select count(*) from delivery_queue').fetchone()[0],0)
