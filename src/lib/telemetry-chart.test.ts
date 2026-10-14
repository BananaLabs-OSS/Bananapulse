import {expect,it} from 'vitest';
import {aggregateChartPoints} from './telemetry-chart';
it('uses weighted hourly count means while preserving extremes and gaps',()=>{
 const metric={id:'players',label:'Players',unit:'count'};
 const points=aggregateChartPoints([{moduleId:'a',at:3600,bucket:900,metric:{...metric,value:4,samples:30,min:0,max:8}},
  {moduleId:'a',at:4500,bucket:0,metric:{...metric,value:10}},
  {moduleId:'a',at:10800,bucket:0,metric:{...metric,value:2}}],3600);
 expect(points).toHaveLength(2);expect(points[0].metric).toMatchObject({value:130/31,min:0,max:10,samples:31});
 expect(points.map(point=>point.at)).toEqual([3600,10800]);
});
