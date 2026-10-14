/** Optional telemetry is independent of status quorum; only configured numeric fields are public. */
export interface PublicMetric { id: string; label: string; unit: string; value: number }
export interface TelemetryView { id: string; componentId: string; unavailable: boolean; observedAt?: number; metrics: PublicMetric[] }
interface Module { id: string; componentId: string; url: string; enabled: boolean; publicMetrics: string[] }
export function moduleDefinitions(raw: string | undefined): Module[] {
  if (!raw) return [];
  let items: unknown; try { items = JSON.parse(raw); } catch { return []; }
  if (!Array.isArray(items) || items.length > 16) return [];
  return items.filter((m): m is Module => m && m.enabled === true && typeof m.id === 'string' &&
    typeof m.componentId === 'string' && typeof m.url === 'string' && Array.isArray(m.publicMetrics) &&
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
  const modules = moduleDefinitions(process.env.PULSE_TELEMETRY_MODULES).filter(m => componentIds.includes(m.componentId));
  return Promise.all(modules.map(async module => {
    try {
      const url = new URL(module.url);
      if (url.username || url.password || (url.protocol !== 'https:' && !(url.protocol === 'http:' && ['127.0.0.1','[::1]'].includes(url.hostname)))) throw new Error('Invalid module URL');
      const response = await fetch(url, { headers: { Authorization: `Bearer ${process.env.PULP_BRIDGE_TOKEN ?? ''}` },
        redirect: 'error', cache: 'no-store', signal: AbortSignal.timeout(2000) });
      if (!response.ok) throw new Error('Module unavailable');
      // Bound consumption of an optional module response independently of page status.
      const reader = response.body?.getReader(); if (!reader) throw new Error('Empty response');
      const chunks: Uint8Array[] = []; let size = 0;
      try { while (true) { const part = await reader.read(); if (part.done) break; size += part.value.length;
        if (size > 16384) { await reader.cancel(); throw new Error('Module response too large'); } chunks.push(part.value); } }
      finally { reader.releaseLock(); }
      const raw = new Uint8Array(size); let offset = 0; for (const part of chunks) { raw.set(part, offset); offset += part.length; }
      return publicSnapshot(module, JSON.parse(new TextDecoder().decode(raw)));
    } catch { return { id: module.id, componentId: module.componentId, unavailable: true, metrics: [] }; }
  }));
}
