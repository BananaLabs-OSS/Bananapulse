"""Game-server monitoring module: adapters normalize protocols; shared telemetry owns lifecycle."""
import copy
from .adapters import space_engineers,steam_query
ADAPTERS={'game-server/space-engineers':space_engineers,'game-server/steam-query':steam_query}

def resolve(ident):
    if ident not in ADAPTERS: raise ValueError('Unknown game-server adapter')
    return ADAPTERS[ident]

def configure(config,ident):
    adapter=resolve(ident);result=copy.deepcopy(config)
    result['adapter']=ident
    result.setdefault('metrics',copy.deepcopy(adapter.METRICS))
    return result

def collect(config,ident):
    return resolve(ident).collect(config)
