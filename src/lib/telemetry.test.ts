import { expect, it } from 'vitest';
import { moduleDefinitions, publicSnapshot } from './telemetry';
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
