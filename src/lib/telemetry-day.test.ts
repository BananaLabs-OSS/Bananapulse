import {expect,it} from 'vitest';
import {dayBreakdown,chartKind} from './telemetry-day';
it('assigns an incident only to hours it overlaps, clipping midnight and ongoing incidents',()=>{
 const base={id:'a',title:'Outage',severity:'major',affects:['game'],start:'2026-10-13',end:'2026-10-14'};
 const incidents=[{...base,startedAt:'2026-10-13T23:59:50Z',resolvedAt:'2026-10-14T00:00:20Z'},
 {...base,id:'boundary',startedAt:'2026-10-13T23:50:00Z',resolvedAt:'2026-10-14T00:00:00Z'},
 {...base,id:'open',startedAt:'2026-10-14T01:30:00Z'},
 {...base,id:'other',affects:['other'],startedAt:'2026-10-14T00:00:00Z'}];
 const now=Date.parse('2026-10-14T02:10:00Z')/1000;
 const day=dayBreakdown('2026-10-14',undefined,incidents,['game'],now);
 expect(day.hours[0].incidents.map(i=>i.incident.id)).toEqual(['a']);
 expect(day.hours[0].incidents[0].until-day.hours[0].incidents[0].from).toBe(20);
 expect(day.hours[1].incidents.map(i=>i.incident.id)).toEqual(['open']);expect(day.hours[2].incidents[0].until).toBe(now);
 expect(day.hours[3]).toMatchObject({future:true,metrics:[],incidents:[]});
});
it('keeps date-only incidents separate instead of inventing hourly timestamps',()=>{
 const incident={id:'a',title:'Historical outage',severity:'major',affects:['game'],start:'2026-10-14',end:'2026-10-14'};
 const day=dayBreakdown('2026-10-14',undefined,[incident],['game'],Date.parse('2026-10-15T00:00:00Z')/1000);
 expect(day.untimedIncidents).toEqual([incident]);expect(day.hours.every(h=>h.incidents.length===0)).toBe(true);
 expect(()=>dayBreakdown('2026-02-30',undefined,[],[])).toThrow();
});
it('uses graph geometry appropriate to each metric type',()=>{
 expect(chartKind('count',false)).toBe('step');expect(chartKind('count',true)).toBe('bars');
 expect(chartKind('boolean',true)).toBe('state');
 for(const unit of ['ratio','ms','reported'])expect(chartKind(unit,true)).toBe('line');
});
