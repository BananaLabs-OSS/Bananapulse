"""Explicit provider registry: no configurable Python imports or arbitrary executable plugins."""
from . import http_json

def adapter_id(config):
    if 'adapter' in config: return config['adapter']
    # Existing installations retain their configuration and snapshot identity.
    auth=config.get('auth','none')
    if auth=='vrage-hmac-sha1': return 'game-server/space-engineers'
    if auth=='none': return 'http-json'
    raise ValueError('Unsupported collector authentication')

def configure(config):
    ident=adapter_id(config)
    if ident=='http-json': return {**config,'adapter':ident}
    if ident.startswith('game-server/'):
        from game_server import configure as game_configure
        return game_configure(config,ident)
    raise ValueError('Unknown telemetry adapter')

def collect(config):
    ident=adapter_id(config)
    if ident=='http-json':
        if config.get('auth','none')!='none': raise ValueError('Unsupported JSON authentication')
        return http_json.fetch(config)
    if ident.startswith('game-server/'):
        from game_server import collect as game_collect
        return game_collect(config,ident)
    raise ValueError('Unknown telemetry adapter')
