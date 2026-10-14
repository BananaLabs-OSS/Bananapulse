import {afterEach,expect,it,vi} from 'vitest';
import {filterHistory,publicTelemetryHistory} from './telemetry-history';
const module={id:'history-test',componentId:'private',displayComponentId:'public',transport:'pulp' as const,enabled:true,publicHistory:true,publicMetrics:['speed']};
const metric={id:'speed',label:'Speed',unit:'ratio',value:1,min:.5,max:1.5};
afterEach(()=>{vi.unstubAllEnvs();vi.unstubAllGlobals();});
it('filters private fields, invalid timestamps and malformed ranges while preserving historical gaps',()=>{
 const result=filterHistory(module,[{observedAt:100,bucketSeconds:900,private:'secret',metrics:[metric,{...metric,id:'private'}]},{observedAt:20000,metrics:[metric]},{observedAt:1000,metrics:[{...metric,min:2}]}],0,2000);
 expect(result).toEqual([{observedAt:100,bucketSeconds:900,metrics:[metric]},{observedAt:1000,metrics:[{id:'speed',label:'Speed',unit:'ratio',value:1}]}]);
});
it('is opt-in and combines week summaries with recent raw samples without duplicate buckets',async()=>{
 vi.stubEnv('PULSE_TELEMETRY_MODULES',JSON.stringify([module]));vi.stubEnv('PULP_BRIDGE_URL','http://127.0.0.1:8788');
 const now=1000000;
 const fetcher=vi.fn().mockImplementation((_url,request)=>{
  const q=JSON.parse(request.body);return Promise.resolve(new Response(JSON.stringify({version:'telemetry.v1',samples:q.bucketSeconds && q.now<now-90000 ? [] : q.bucketSeconds ? [{observedAt:now-90000,bucketSeconds:900,metrics:[metric]},{observedAt:now-900,bucketSeconds:900,metrics:[metric]}] : [{observedAt:now-1000,metrics:[metric]},{observedAt:now-100,metrics:[metric]}]})));
 });vi.stubGlobal('fetch',fetcher);
 expect(await publicTelemetryHistory(['other'],now)).toEqual([]);
 const views=await publicTelemetryHistory(['public'],now);
 expect(views[0].samples.map(s=>s.observedAt)).toEqual([now-90000,now-1000,now-100]);
 expect(fetcher.mock.calls.map(call=>JSON.parse(call[1].body).bucketSeconds)).toEqual([900,undefined,900]);
 expect(JSON.stringify(views)).not.toContain('private');
 await publicTelemetryHistory(['public'],now+1);expect(fetcher).toHaveBeenCalledTimes(3);
});
it('keeps unavailable history independent from live status',async()=>{
 vi.stubEnv('PULSE_TELEMETRY_MODULES',JSON.stringify([{...module,id:'failed-history'}]));vi.stubEnv('PULP_BRIDGE_URL','http://127.0.0.1:8788');vi.stubGlobal('fetch',vi.fn().mockRejectedValue(new Error('offline')));
 expect((await publicTelemetryHistory(['public'],2000000))[0]).toMatchObject({unavailable:true,samples:[]});
});
