import { moduleDefinitions, type Module, type PublicMetric, fetchModule } from './telemetry';

export interface HistorySample { observedAt: number; bucketSeconds?: number; metrics: PublicMetric[] }
export interface HistoryView { id: string; unavailable: boolean; truncated: boolean; samples: HistorySample[] }
const pending = new Map<string, Promise<HistoryView>>();
const cache = new Map<string, { at: number; value: HistoryView }>();

/** Historical values are independent of the current poll state. Only explicitly public fields survive. */
export function filterHistory(module: Module, payload: unknown, since: number, until: number): HistorySample[] {
  if (payload && typeof payload === 'object' && !Array.isArray(payload)) {
    const envelope=payload as {version?:unknown;samples?:unknown};
    if(envelope.version!=='telemetry.v1') throw new Error('Invalid history version');
    payload=envelope.samples;
  }
  if (!Array.isArray(payload) || payload.length > 2000) throw new Error('Invalid history');
  const samples: HistorySample[] = [];
  for (const sample of payload) {
    if (!sample || !Number.isInteger(sample.observedAt) || sample.observedAt < since || sample.observedAt > until || !Array.isArray(sample.metrics) || sample.metrics.length > 32) continue;
    const metrics: PublicMetric[] = sample.metrics.filter((m: any) => m && module.publicMetrics.includes(m.id) && typeof m.label === 'string' && m.label.length <= 80 && typeof m.unit === 'string' && m.unit.length <= 32 && Number.isFinite(m.value) && typeof m.value === 'number')
      .map((m: any) => ({ id: m.id, label: m.label, unit: m.unit, value: m.value, ...(Number.isFinite(m.min) && Number.isFinite(m.max) && m.min <= m.value && m.max >= m.value ? {min:m.min,max:m.max} : {}) }));
    if (metrics.length) samples.push({ observedAt: sample.observedAt, ...(Number.isInteger(sample.bucketSeconds) && sample.bucketSeconds>=60 && sample.bucketSeconds<=3600 ? {bucketSeconds:sample.bucketSeconds} : {}), metrics });
  }
  return samples;
}

export async function publicTelemetryHistory(componentIds: string[], now = Math.floor(Date.now()/1000)): Promise<HistoryView[]> {
  const modules = moduleDefinitions(process.env.PULSE_TELEMETRY_MODULES).filter(m => m.publicHistory === true && componentIds.includes(m.displayComponentId ?? m.componentId) && (m.transport === 'pulp' || typeof m.historyUrl === 'string'));
  return Promise.all(modules.map(async module => {
    const key = JSON.stringify([module, process.env.PULP_BRIDGE_URL]);
    const cached = cache.get(key); if (cached && now-cached.at >= 0 && now-cached.at < 30) return cached.value;
    const existing=pending.get(key); if(existing) return existing;
    const task=(async () => {
    const since = now-7*86400;
    const signal = AbortSignal.timeout(6000);
    const samples = new Map<number, HistorySample>(); let truncated = false; let unavailable = false;
    try {
      async function summaries() {
        const all: HistorySample[]=[]; let cursor=now;
        for(let page=0;page<4;page++) {
          const payload=await fetchModule(module,'history',cursor,since,signal,900);
          const rows=filterHistory(module,payload,since-900,cursor); all.push(...rows);
          if(!rows.length || rows[0].observedAt<=since) return all;
          cursor=Math.min(...rows.map(row=>row.observedAt))-1;
          if(page===3) truncated=true;
        }
        return all;
      }
      const requests = module.transport === 'pulp'
        ? [summaries(), fetchModule(module, 'history', now, now-3600, signal)]
        : [fetchModule(module, 'history', now, now-86400, signal)];
      const results = await Promise.allSettled(requests);
      for (let i=0;i<results.length;i++) {
        const result=results[i]; if(result.status==='rejected') { unavailable=true;continue; }
        for (const sample of filterHistory(module,result.value,since-900,now)) samples.set(sample.observedAt,sample);
      }
      // Prefer raw samples over summaries for the most recent hour.
      const rawStart = Math.min(...[...samples.values()].filter(s=>!s.bucketSeconds).map(s=>s.observedAt));
      for (const [at,sample] of samples) if(sample.bucketSeconds && at>=rawStart) samples.delete(at);
    } catch { unavailable = true; }
    const value = { id: module.id, unavailable, truncated, samples: [...samples.values()].sort((a,b)=>a.observedAt-b.observedAt) };
    if (cache.size > 32) cache.clear();
    cache.set(key, { at: now, value }); return value;
    })();
    pending.set(key,task);
    try {return await task;} finally {pending.delete(key);}
  }));
}
