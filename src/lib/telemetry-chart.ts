import {formatMetric,type PublicMetric} from './telemetry';
import {chartKind} from './telemetry-day';
export interface ChartPoint {moduleId:string;at:number;bucket:number;metric:PublicMetric}
export interface IncidentBand {id:string;title:string;severity:string;from:number;until:number}
const ns='http://www.w3.org/2000/svg';
export function svgElement(name:string,attrs:Record<string,string|number>) {const el=document.createElementNS(ns,name);for(const [key,value] of Object.entries(attrs))el.setAttribute(key,String(value));return el;}
export function utc(at:number) {return new Date(at*1000).toISOString().replace('T',' ').replace('.000Z',' UTC');}
function stateText(metric:PublicMetric) {return metric.value===1 ? 'Yes' : metric.value===0 ? 'No' : `${(metric.value*100).toFixed(1)}% yes checks`;}
export function chartCard(points:ChartPoint[],start:number,end:number,bands:IncidentBand[],mean?:PublicMetric):HTMLElement {
 const card=document.createElement('article');card.className='history-chart';
 const title=document.createElement('h4');title.textContent=points[0].metric.label;
 const latest=points[points.length-1],unit=latest.metric.unit;
 const averaged=points.every(point=>point.bucket>0);const kind=chartKind(unit,averaged);card.dataset.chartKind=kind;
 const summary=document.createElement('p');summary.className='chart-summary';
 const low=Math.min(...points.map(p=>p.metric.min??p.metric.value)),high=Math.max(...points.map(p=>p.metric.max??p.metric.value));
 summary.textContent=unit==='boolean' ? `Latest ${stateText(latest.metric)} · gaps mean no recorded state` : `${mean ? 'Day average '+formatMetric(mean) : 'Latest '+formatMetric(latest.metric)} · Min ${formatMetric({...latest.metric,value:low})} · Max ${formatMetric({...latest.metric,value:high})}`;
 const bottom=Math.min(0,low),maximum=unit==='ratio' ? Math.max(1,high):high,top=maximum===bottom ? bottom+1:maximum;
 const x=(at:number)=>44+(at-start)/(end-start)*636,y=(value:number)=>150-(value-bottom)/(top-bottom)*126;
 const svg=svgElement('svg',{viewBox:'0 0 700 190',role:'img',tabindex:'0','aria-label':`${points[0].metric.label}: ${kind} chart. Use arrow keys to inspect recorded samples.`});
 const axis=svgElement('text',{x:44,y:12});axis.textContent=({ratio:'×',count:'count',reported:'reported load',boolean:'state'} as Record<string,string>)[unit]??unit;svg.append(axis);
 if(kind!=='state') for(const value of [...new Set(kind==='step' ? [bottom,Math.ceil((bottom+top)/2),Math.ceil(top)]:[bottom,(bottom+top)/2,top])]) {
  svg.append(svgElement('line',{x1:44,x2:680,y1:y(value),y2:y(value),class:'chart-grid'}));
  const label=svgElement('text',{x:40,y:y(value)+4,'text-anchor':'end'});label.textContent=value.toLocaleString('en',{maximumFractionDigits:2});svg.append(label);
 }
 if(unit==='ratio'){svg.append(svgElement('line',{x1:44,x2:680,y1:y(1),y2:y(1),class:'chart-reference'}));const label=svgElement('title',{});label.textContent='Unity reference: 1×';svg.append(label);}
 for(const band of bands) {
  const from=Math.max(start,band.from),until=Math.min(end,band.until);if(until<=from)continue;
  const rect=svgElement('rect',{x:x(from),y:20,width:Math.max(1,x(until)-x(from)),height:134,class:'chart-incident',fill:band.severity==='major'?'#ef4444':'#f59e0b'});
  const label=svgElement('title',{});label.textContent=`${band.title} · ${utc(from)}–${utc(until)}`;rect.append(label);svg.append(rect);
 }
 const intervals=points.slice(1).map((p,i)=>!p.bucket&&!points[i].bucket ? p.at-points[i].at : 0).filter(n=>n>0).sort((a,b)=>a-b);
 const interval=intervals.length ? intervals[Math.floor(intervals.length/2)] : 30;
 const segments:ChartPoint[][]=[];
 for(const point of points) {const segment=segments[segments.length-1],previous=segment?.[segment.length-1];if(!previous || point.at-previous.at>Math.max(previous.bucket,point.bucket,interval)*1.8)segments.push([point]);else segment.push(point);}
 if(kind==='state') {
  for(const point of points) {
   const left=Math.max(start,point.at),right=Math.min(end,point.at+(point.bucket||interval));if(right<=left)continue;
   svg.append(svgElement('rect',{x:x(left),y:52,width:Math.max(1,x(right)-x(left)-.5),height:50,class:'chart-state',fill:point.metric.value===1?'#22c55e':point.metric.value===0?'#ef4444':'#f59e0b'}));
  }
 } else if(kind==='bars') {
  for(const point of points) {
   const left=Math.max(start,point.at),right=Math.min(end,point.at+point.bucket);if(right<=left)continue;
   const middle=(x(left)+x(right))/2;
   svg.append(svgElement('rect',{x:x(left)+1,y:Math.min(y(0),y(point.metric.value)),width:Math.max(1,x(right)-x(left)-2),height:Math.max(1,Math.abs(y(0)-y(point.metric.value))),class:'chart-average-bar'}));
   svg.append(svgElement('line',{x1:middle,x2:middle,y1:y(point.metric.min??point.metric.value),y2:y(point.metric.max??point.metric.value),class:'chart-whisker'}));
   for(const value of [point.metric.min??point.metric.value,point.metric.max??point.metric.value])svg.append(svgElement('line',{x1:middle-3,x2:middle+3,y1:y(value),y2:y(value),class:'chart-whisker'}));
  }
 } else for(const segment of segments) {
  if(kind==='step') {
   let path=`M ${x(segment[0].at)} ${y(segment[0].metric.value)}`;
   for(const point of segment.slice(1))path+=` H ${x(point.at)} V ${y(point.metric.value)}`;
   const last=segment[segment.length-1];path+=` H ${x(Math.min(end,last.at+interval))}`;
   svg.append(svgElement('path',{d:path,class:'chart-line chart-step'}));
  } else {
   const px=(p:ChartPoint)=>x(Math.min(end,p.at+(p.bucket?p.bucket/2:0)));
   const band=segment.map(p=>`${px(p)},${y(p.metric.max??p.metric.value)}`).concat([...segment].reverse().map(p=>`${px(p)},${y(p.metric.min??p.metric.value)}`)).join(' ');
   svg.append(svgElement('polygon',{points:band,class:'chart-band'}));
   svg.append(svgElement('polyline',{points:segment.map(p=>`${px(p)},${y(p.metric.value)}`).join(' '),class:'chart-line'}));
   if(segment.length===1)svg.append(svgElement('circle',{cx:px(segment[0]),cy:y(segment[0].metric.value),r:3,class:'chart-dot'}));
  }
 }
 for(let i=0;i<=4;i++) {
  const at=start+(end-start)*i/4,label=svgElement('text',{x:x(at),y:177,'text-anchor':i===0?'start':i===4?'end':'middle'});
  label.textContent=new Date(at*1000).toISOString().slice(end-start>86400?5:11,16).replace('T',' ');svg.append(label);
 }
 const cursor=svgElement('line',{x1:0,x2:0,y1:18,y2:154,class:'chart-cursor',visibility:'hidden'});svg.append(cursor);
 const detail=document.createElement('p');detail.className='chart-detail';detail.setAttribute('aria-live','polite');detail.textContent=kind==='state'?'Yes / No / Mixed state. Gaps = no data.':kind==='bars'?'Bars = interval averages; whiskers = minimum–maximum.':kind==='step'?'Steps = recorded discrete counts.':'Line = recorded value or interval mean; shade = minimum–maximum.';
 let index=points.length-1;
 function inspect(i:number) {
  index=Math.max(0,Math.min(points.length-1,i));const point=points[index];cursor.setAttribute('x1',String(x(point.at)));cursor.setAttribute('x2',String(x(point.at)));cursor.setAttribute('visibility','visible');
  window.dispatchEvent(new CustomEvent('pulse:hour-hover', {detail:{at:point.at,moduleId:point.moduleId}}));
  detail.textContent=`${utc(point.at)}${point.bucket?'–'+new Date((point.at+point.bucket)*1000).toISOString().slice(11,16)+' UTC':''} · ${unit==='boolean'?stateText(point.metric):formatMetric(point.metric)}${point.bucket && unit!=='boolean' ? ` average · range ${formatMetric({...point.metric,value:point.metric.min??point.metric.value})}–${formatMetric({...point.metric,value:point.metric.max??point.metric.value})}`:''}`;
 }
 svg.addEventListener('pointermove',event=>{const e=event as PointerEvent,r=svg.getBoundingClientRect(),at=start+((e.clientX-r.left)/r.width*700-44)/636*(end-start);let nearest=0;for(let i=1;i<points.length;i++)if(Math.abs(points[i].at-at)<Math.abs(points[nearest].at-at))nearest=i;inspect(nearest);});
 svg.addEventListener('keydown',event=>{const e=event as KeyboardEvent;if(e.key==='ArrowLeft'||e.key==='ArrowRight'){e.preventDefault();inspect(index+(e.key==='ArrowRight'?1:-1));}});
 card.append(title,summary,svg,detail);return card;
}

/** Combine saved checks/buckets using sample weights, keeping count means distinct from raw counts. */
export function aggregateChartPoints(points:ChartPoint[],bucketSeconds:number):ChartPoint[] {
 if(!bucketSeconds)return points.sort((a,b)=>a.at-b.at);
 const buckets=new Map<string,ChartPoint>();
 for(const point of points) {
  const at=Math.floor(point.at/bucketSeconds)*bucketSeconds,key=`${point.moduleId}/${point.metric.id}/${at}`;
  const previous=buckets.get(key),metric=point.metric,weight=metric.samples??1,oldWeight=previous?.metric.samples??0;
  buckets.set(key,{moduleId:point.moduleId,at,bucket:bucketSeconds,metric:{...metric,
   value:previous ? (previous.metric.value*oldWeight+metric.value*weight)/(oldWeight+weight):metric.value,
   min:Math.min(previous?.metric.min??metric.min??metric.value,metric.min??metric.value),
   max:Math.max(previous?.metric.max??metric.max??metric.value,metric.max??metric.value),samples:oldWeight+weight}});
 }
 return [...buckets.values()].sort((a,b)=>a.at-b.at);
}
