import type { HistoryView } from './telemetry-history';
import type { PublicMetric } from './telemetry';
export interface MetricSummary extends PublicMetric { min:number; max:number; samples:number }
export interface DaySummary { date:string; metrics:MetricSummary[]; hours:{ hour:number; metrics:MetricSummary[] }[] }
export function dailySummaries(histories:HistoryView[], now=Math.floor(Date.now()/1000)):DaySummary[] {
 const days=new Map<string,Map<number,Map<string,MetricSummary>>>();
 for(let at=Math.floor((now-7*86400)/86400)*86400;at<=now;at+=86400) days.set(new Date(at*1000).toISOString().slice(0,10),new Map());
 function merge(map:Map<string,MetricSummary>,metric:PublicMetric) {
  const weight=metric.samples??1;const low=metric.min??metric.value,high=metric.max??metric.value;
  const old=map.get(metric.id);
  map.set(metric.id,{...metric,value:old ? (old.value*old.samples+metric.value*weight)/(old.samples+weight) : metric.value,min:Math.min(old?.min??low,low),max:Math.max(old?.max??high,high),samples:(old?.samples??0)+weight});
 }
 for(const history of histories) for(const sample of history.samples) {
  if(sample.observedAt<now-7*86400 || sample.observedAt>now) continue;
  const date=new Date(sample.observedAt*1000).toISOString().slice(0,10),day=days.get(date);if(!day)continue;
  const hour=Math.floor(sample.observedAt/3600)%24;
  const metrics=day.get(hour)??new Map<string,MetricSummary>();day.set(hour,metrics);
  for(const metric of sample.metrics) if(metric.unit!=='boolean')merge(metrics,{...metric,id:`${history.id}/${metric.id}`});
 }
 return [...days].reverse().map(([date,day])=>{
  const total=new Map<string,MetricSummary>();
  const hours=Array.from({length:24},(_,hour)=>{const metrics=[...(day.get(hour)?.values()??[])];for(const metric of metrics)merge(total,metric);return {hour,metrics};});
  return {date,metrics:[...total.values()],hours};
 });
}
