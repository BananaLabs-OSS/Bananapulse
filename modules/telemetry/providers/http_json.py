"""Shared bounded, read-only HTTPS transport; provider authentication is supplied externally."""
import json
import urllib.request
from urllib.parse import urlsplit

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args): return None

def validate_url(url):
    parsed = urlsplit(url)
    if parsed.scheme != 'https' or not parsed.hostname or parsed.username or parsed.password or parsed.fragment:
        raise ValueError('Collector requires a credential-free HTTPS URL')
    return parsed

def fetch(config, headers=None):
    validate_url(config['url'])
    request = urllib.request.Request(config['url'], headers={'Accept':'application/json', **(headers or {})}, method='GET')
    with urllib.request.build_opener(NoRedirect).open(request, timeout=10) as response:
        body=response.read(65537)
        if len(body)>65536: raise ValueError('Telemetry response exceeds limit')
        return json.loads(body)
