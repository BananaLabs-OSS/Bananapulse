"""Generic Steam A2S_INFO adapter. Split/compressed replies are explicitly unsupported."""
import socket,struct,time
QUERY=b'\xff\xff\xff\xffTSource Engine Query\x00'
METRICS=[{'id':i,'label':label,'unit':unit,'path':'data.'+i} for i,label,unit in
         [('latency_ms','Query response time','ms'),('players','Players','count'),
          ('max_players','Player capacity','count'),('bots','Bots','count')]]

def parse_info(data, expected_game=None):
    if len(data)<6 or data[:5]!=b'\xff\xff\xff\xffI': raise ValueError('Invalid A2S_INFO response')
    offset=6;strings=[]
    for _ in range(4):
        end=data.index(b'\x00',offset);strings.append(data[offset:end].decode('utf-8','replace'));offset=end+1
    _,players,maximum,bots=struct.unpack_from('<HBBB',data,offset)
    if expected_game and expected_game.casefold() not in strings[3].casefold():
        raise ValueError('Unexpected game query response')
    return {'server_name':strings[0],'map':strings[1],'players':players,'max_players':maximum,'bots':bots}

def query(host,port,expected_game=None,timeout=4):
    started=time.monotonic()
    with socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as sock:
        sock.settimeout(timeout);sock.connect((host,port));sock.send(QUERY);data=sock.recv(65535)
        if data[:5]==b'\xff\xff\xff\xffA':
            if len(data)!=9: raise ValueError('Invalid A2S challenge')
            sock.send(QUERY+data[5:9]);data=sock.recv(65535)
        info=parse_info(data,expected_game);info['latency_ms']=round((time.monotonic()-started)*1000)
        return info

def collect(config):
    return {'data':query(config['host'],int(config['queryPort']),config.get('expectedGame'))}
