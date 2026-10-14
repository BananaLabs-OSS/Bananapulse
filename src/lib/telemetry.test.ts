import { expect, it, vi, afterEach } from 'vitest';
import { moduleDefinitions, publicSnapshot, publicTelemetry, formatMetric } from './telemetry';
const module = { id:'test', componentId:'server', url:'http://127.0.0.1:8790/snapshot', enabled:true, publicMetrics:['speed'] };
const payload = { version:'telemetry.v1',moduleId:'test',componentId:'server',stale:false,lastPollOk:true,observedAt:100,expiresAt:220,
 metrics:[{id:'speed',label:'Simulation speed',unit:'ratio',value:1},{id:'private',label:'Private load',unit:'reported',value:42}] };
it('is opt-in and exposes only declared numeric fields',()=>{
 expect(moduleDefinitions(undefined)).toEqual([]);expect(moduleDefinitions(JSON.stringify([{...module,enabled:false}]))).toEqual([]);
 expect(publicSnapshot(module,payload,110).metrics.map(m=>m.id)).toEqual(['speed']);
});
it('rejects stale, failed, mismatched and future samples without changing service status',()=>{
 for(const change of [{stale:true},{lastPollOk:false},{componentId:'other'},{observedAt:130},{expiresAt:109}])
  expect(publicSnapshot(module,{...payload,...change},110).unavailable).toBe(true);
});

afterEach(() => { vi.unstubAllEnvs(); vi.unstubAllGlobals(); });
it('attaches private component stats to a visible service while retaining snapshot identity', async () => {
 vi.stubEnv('PULSE_TELEMETRY_MODULES', JSON.stringify([{...module, transport:'pulp', url:undefined, displayComponentId:'game'}]));
 vi.stubEnv('PULP_BRIDGE_URL','http://127.0.0.1:8788');
 const now = Math.floor(Date.now()/1000);
 const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({...payload,observedAt:now,expiresAt:now+120})));
 vi.stubGlobal('fetch',fetcher);
 const views = await publicTelemetry(['game']);
 expect(views[0].metrics.map(m=>m.id)).toEqual(['speed']);
 expect(JSON.parse(fetcher.mock.calls[0][1].body).moduleId).toBe('test');
 expect(await publicTelemetry(['unrelated'])).toEqual([]);
 expect(publicSnapshot({...module,displayComponentId:'game'},{...payload,componentId:'game'},110).unavailable).toBe(true);
});
it('keeps optional transport failures isolated and rejects oversized responses', async () => {
 vi.stubEnv('PULSE_TELEMETRY_MODULES',JSON.stringify([module]));
 vi.stubGlobal('fetch',vi.fn().mockResolvedValue(new Response(' '.repeat(16385))));
 expect((await publicTelemetry(['server']))[0].unavailable).toBe(true);
 vi.stubGlobal('fetch',vi.fn().mockRejectedValue(new Error('offline')));
 expect((await publicTelemetry(['server']))[0].metrics).toEqual([]);
});
it('formats counts, simulation ratios, readiness and reported load without inventing percentages',()=>{
 const metric={id:'x',label:'x',unit:'count',value:1234};
 expect(formatMetric(metric)).toBe('1,234');
 expect(formatMetric({...metric,unit:'ratio',value:1.02})).toBe('1.02×');
 expect(formatMetric({...metric,unit:'boolean',value:1})).toBe('Yes');
 expect(formatMetric({...metric,unit:'reported',value:56.8})).toBe('56.8');
});
