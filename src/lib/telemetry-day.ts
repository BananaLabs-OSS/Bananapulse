import type { DayIncident } from './view';
import type { DaySummary, MetricSummary } from './telemetry-periods';
export interface HourIncident { incident:DayIncident; from:number; until:number; active:boolean }
export interface DayBreakdown { date:string; metrics:MetricSummary[]; hours:{hour:number;metrics:MetricSummary[];incidents:HourIncident[];future:boolean}[];untimedIncidents:DayIncident[] }
export function dayBreakdown(date:string, summary:DaySummary|undefined, incidents:DayIncident[], componentIds:string[], now=Date.now()/1000):DayBreakdown {
 const start=Date.parse(date+'T00:00:00Z')/1000;
 if(!/^\d{4}-\d{2}-\d{2}$/.test(date) || !Number.isFinite(start) || new Date(start*1000).toISOString().slice(0,10)!==date) throw new Error('Invalid day');
 const matching=incidents.filter(i=>i.affects.some(id=>componentIds.includes(id)) && i.start<=date && i.end>=date);
 const untimedIncidents:DayIncident[]=[];
 const timed=matching.flatMap(incident=>{
  const from=incident.startedAt?.includes('T') ? Date.parse(incident.startedAt)/1000 : NaN;
  const until=incident.resolvedAt?.includes('T') ? Date.parse(incident.resolvedAt)/1000 : incident.resolvedAt ? NaN : now;
  if(!Number.isFinite(from)||!Number.isFinite(until)||until<from){untimedIncidents.push(incident);return [];}
  return [{incident,from,until:Math.min(until,now),active:!incident.resolvedAt}];
 });
 return {date,metrics:summary?.metrics??[],untimedIncidents,hours:Array.from({length:24},(_,hour)=>{
  const from=start+hour*3600,until=from+3600;
  return {hour,metrics:summary?.hours[hour]?.metrics??[],future:from>now,
   incidents:timed.filter(i=>i.from<until && (i.until>from || (i.active && i.from===from && i.from<=now))).map(i=>({...i,from:Math.max(from,i.from),until:Math.min(until,i.until)}))};
 })};
}
export function chartKind(unit:string, averaged:boolean):'state'|'bars'|'step'|'line' {
 if(unit==='boolean')return 'state';
 if(unit==='count')return averaged ? 'bars' : 'step';
 return 'line';
}
