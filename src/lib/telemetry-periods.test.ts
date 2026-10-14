import {expect,it} from 'vitest';
import {dailySummaries} from './telemetry-periods';
it('weights hourly and daily means by recorded checks, preserves extremes and leaves missing hours empty',()=>{
 const now=Date.parse('2026-10-14T03:00:00Z')/1000;
 const metric={id:'speed',label:'Speed',unit:'ratio'};
 const days=dailySummaries([{id:'test',unavailable:false,truncated:false,samples:[
  {observedAt:now-7200,bucketSeconds:900,metrics:[{...metric,value:.8,min:.4,max:1,samples:30}]},
  {observedAt:now-7170,metrics:[{...metric,value:1}]},
  {observedAt:now-3600,bucketSeconds:900,metrics:[{...metric,value:1,min:1,max:1,samples:10}]}
 ]}],now);
 const today=days[0];expect(today.date).toBe('2026-10-14');expect(today.hours).toHaveLength(24);
 expect(today.metrics[0]).toMatchObject({samples:41,min:.4,max:1});expect(today.metrics[0].value).toBeCloseTo(35/41);
 expect(today.hours[0].metrics).toEqual([]);expect(today.hours[1].metrics[0].samples).toBe(31);
 expect(days[1].metrics).toEqual([]);
});
it('keeps identically named metrics from different game modules separate',()=>{
 const now=Date.parse('2026-10-14T03:00:00Z')/1000;
 const histories=['game-a','game-b'].map((id,index)=>({id,unavailable:false,truncated:false,samples:[{observedAt:now-30,metrics:[{id:'players',label:'Players',unit:'count',value:index+1}]}]}));
 expect(dailySummaries(histories,now)[0].metrics.map(m=>[m.id,m.value])).toEqual([['game-a/players',1],['game-b/players',2]]);
});
