/** Optional telemetry is independent of status quorum; only configured numeric fields are public. */
export interface PublicMetric { id: string; label: string; unit: string; value: number; min?: number; max?: number }
export interface TelemetryView { id: string; componentId: string; unavailable: boolean; observedAt?: number; metrics: PublicMetric[] }
export interface Module { id: string; componentId: string; displayComponentId?: string; transport?: 'http' | 'pulp'; url?: string; historyUrl?: string; publicHistory?: boolean; enabled: boolean; publicMetrics: string[] }
export function moduleDefinitions(raw: string | undefined): Module[] {
  if (!raw) return [];
  let items: unknown; try { items = JSON.parse(raw); } catch { return []; }
  if (!Array.isArray(items) || items.length > 16) return [];
  return items.filter((m): m is Module => m && m.enabled === true && typeof m.id === 'string' &&
    typeof m.componentId === 'string' && (m.displayComponentId === undefined || typeof m.displayComponentId === 'string') &&
    (m.transport === 'pulp' || ((m.transport === undefined || m.transport === 'http') && typeof m.url === 'string')) && Array.isArray(m.publicMetrics) &&
    m.publicMetrics.length > 0 && m.publicMetrics.length <= 32 && m.publicMetrics.every((x: unknown) => typeof x === 'string'));
}
export function publicSnapshot(module: Module, payload: any, now = Date.now() / 1000): TelemetryView {
  const empty: TelemetryView = { id: module.id, componentId: module.componentId, unavailable: true, metrics: [] };
  if (payload?.version !== 'telemetry.v1' || payload.moduleId !== module.id || payload.componentId !== module.componentId ||
      payload.stale !== false || payload.lastPollOk !== true || !Number.isFinite(payload.observedAt) || payload.observedAt > now + 5 ||
      !Number.isFinite(payload.expiresAt) || payload.expiresAt <= now || payload.expiresAt <= payload.observedAt ||
      !Array.isArray(payload.metrics) || payload.metrics.length > 32) return empty;
  const metrics = payload.metrics.filter((m: any) => m && module.publicMetrics.includes(m.id) && typeof m.label === 'string' &&
    m.label.length <= 80 && typeof m.unit === 'string' && m.unit.length <= 32 && typeof m.value === 'number' && Number.isFinite(m.value))
    .map((m: any) => ({ id: m.id, label: m.label, unit: m.unit, value: m.value }));
  return { ...empty, unavailable: metrics.length === 0, observedAt: payload.observedAt, metrics };
}
export async function publicTelemetry(componentIds: string[]): Promise<TelemetryView[]> {
  const modules = moduleDefinitions(process.env.PULSE_TELEMETRY_MODULES).filter(m => componentIds.includes(m.displayComponentId ?? m.componentId));
  return Promise.all(modules.map(async module => {
    try {
      const payload = await fetchModule(module, 'snapshot');
      return publicSnapshot(module, payload);
    } catch { return { id: module.id, componentId: module.componentId, unavailable: true, metrics: [] }; }
  }));
}

export function formatMetric(metric: PublicMetric): string {
  if (metric.unit === 'boolean') return metric.value === 1 ? 'Yes' : 'No';
  const value = metric.value.toLocaleString('en', { maximumFractionDigits: 2 });
  if (metric.unit === 'ratio') return `${value}×`;
  if (metric.unit === 'count' || metric.unit === 'reported') return value;
  return `${value} ${metric.unit}`;
}

export async function fetchModule(module: Module, kind: 'snapshot' | 'history', now = Math.floor(Date.now()/1000), since = now-86400, signal?: AbortSignal, bucketSeconds?: number): Promise<unknown> {
      const pulp = module.transport === 'pulp';
      const base = process.env.PULP_BRIDGE_URL?.replace(/\/$/, '');
      const url = new URL(pulp ? `${base}/internal/v1/events/bananapulse.telemetry.${kind}.v1` : (kind === 'history' ? module.historyUrl! : module.url!));
      if (url.username || url.password || (url.protocol !== 'https:' && !(url.protocol === 'http:' && ['127.0.0.1','[::1]'].includes(url.hostname)))) throw new Error('Invalid module URL');
      const token = process.env.PULP_BRIDGE_TOKEN ?? '';
      const response = await fetch(url, {
        method: pulp ? 'POST' : 'GET',
        headers: pulp ? { 'Content-Type': 'application/json', 'X-Pulp-Bridge-Token': token } : { Authorization: `Bearer ${token}` },
        ...(pulp ? { body: JSON.stringify({ version: 'telemetry.v1', moduleId: module.id, now, ...(kind === 'history' ? { since, ...(bucketSeconds ? { bucketSeconds } : {}) } : {}) }) } : {}),
        redirect: 'error', cache: 'no-store', signal: signal ?? AbortSignal.timeout(2000) });
      if (!response.ok) throw new Error('Module unavailable');
      // Bound consumption of an optional module response independently of page status.
      const reader = response.body?.getReader(); if (!reader) throw new Error('Empty response');
      const chunks: Uint8Array[] = []; let size = 0;
      try { while (true) { const part = await reader.read(); if (part.done) break; size += part.value.length;
        if (size > (kind === 'history' ? 524288 : 16384)) { await reader.cancel(); throw new Error('Module response too large'); } chunks.push(part.value); } }
      finally { reader.releaseLock(); }
      const raw = new Uint8Array(size); let offset = 0; for (const part of chunks) { raw.set(part, offset); offset += part.length; }
  return JSON.parse(new TextDecoder().decode(raw));
}
