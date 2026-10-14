import base64,hashlib,hmac,os,sys,unittest
from pathlib import Path
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).parent))
from providers import configure,collect
from game_server.adapters import steam_query,space_engineers
from collector import extract

class AdapterTests(unittest.TestCase):
    def test_legacy_configuration_keeps_identity_and_custom_fields(self):
        old={'id':'vrage','componentId':'existing','auth':'vrage-hmac-sha1','metrics':[{'id':'custom'}]}
        configured=configure(old)
        self.assertEqual(configured['id'],'vrage');self.assertEqual(configured['metrics'],old['metrics'])
        self.assertNotIn('adapter',old)
        self.assertEqual(configured['adapter'],'game-server/space-engineers')
    def test_module_defaults_are_independent_and_only_allowlisted_values_escape(self):
        config=configure({'adapter':'game-server/steam-query'})
        config['metrics'][0]['label']='Changed'
        other=configure({'adapter':'game-server/steam-query'})
        self.assertEqual(other['metrics'][0]['label'],'Query response time')
        metrics=extract({'data':{'latency_ms':70,'players':2,'max_players':16,'bots':0,'secret':'private'}},other['metrics'])
        self.assertEqual([m['id'] for m in metrics],['latency_ms','players','max_players','bots'])
    def test_unknown_adapters_fail_closed(self):
        for ident in ['game-server/missing','python:os.system','unknown']:
            with self.assertRaises(ValueError):configure({'adapter':ident})
    def test_vrage_signing_retains_raw_key_and_exact_path(self):
        with patch.dict(os.environ,{'VRAGE_KEY':'test-raw-key'}),patch.object(space_engineers.secrets,'randbelow',return_value=123),patch.object(space_engineers.email.utils,'formatdate',return_value='fixed-date'),patch.object(space_engineers.http_json,'fetch',return_value={'data':{}}) as fetch:
            collect({'adapter':'game-server/space-engineers','url':'https://example.test/vrageremote/v1/server?x=1'})
            headers=fetch.call_args.args[1]
            signature=base64.b64encode(hmac.new(b'test-raw-key',b'/vrageremote/v1/server?x=1\r\n123\r\nfixed-date\r\n',hashlib.sha1).digest()).decode()
            self.assertEqual(headers['Authorization'],'123:'+signature)
    def test_vrage_rejects_unsafe_url_before_reading_key(self):
        for url in ['http://example.test','https://user:secret@example.test','https://example.test/#fragment']:
            with self.assertRaises(ValueError):collect({'adapter':'game-server/space-engineers','url':url,'keyEnv':'NOT_PRESENT'})
    def test_steam_parser_is_generic_and_optional_game_validation_is_strict(self):
        import struct
        packet=b'\xff\xff\xff\xffI\x11Other server\x00Map\x00folder\x00Another Game\x00'+struct.pack('<HBBB',1,3,16,0)
        self.assertEqual(steam_query.parse_info(packet)['players'],3)
        with self.assertRaises(ValueError):steam_query.parse_info(packet,'Space Engineers')
