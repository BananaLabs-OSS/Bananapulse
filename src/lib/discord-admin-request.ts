import { readAdminJSON } from './admin-json';

/** Retry only reads. Report bounded response metadata, never webhook URLs or bodies. */
export async function discordAdminRequest(body?: { action: string; [key: string]: unknown }): Promise<any> {
  const operation = body?.action ?? 'load';
  const attempts = body ? 1 : 3;
  for (let attempt = 0; attempt < attempts; attempt++) {
    const requestId = crypto.randomUUID();
    let response: Response | undefined;
    try {
      response = await fetch('/api/v1/admin/discord', {
        method: body ? 'POST' : 'GET', cache: 'no-store',
        headers: { 'X-Pulse-Request-ID': requestId, ...(body ? { 'Content-Type': 'application/json' } : {}) },
        ...(body ? { body: JSON.stringify(body) } : {}),
      });
      return await readAdminJSON(response);
    } catch (error) {
      const status = response?.status ?? 0;
      const rawRay = response?.headers.get('cf-ray') ?? '';
      const ray = /^[a-zA-Z0-9-]{0,64}$/.test(rawRay) ? rawRay : '';
      // A separate same-origin request may succeed when an individual edge request fails.
      void fetch('/api/v1/admin/discord', {
        method: 'POST', cache: 'no-store', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action: 'diagnostic', operation, status, requestId, ray }),
      }).catch(() => {});
      if (!body && (status === 0 || status >= 500) && attempt + 1 < attempts) {
        await new Promise(resolve => setTimeout(resolve, 750 * (attempt + 1)));
        continue;
      }
      const message = error instanceof Error ? error.message : 'Request failed.';
      throw new Error(`${operation === 'load' ? 'Loading settings' : operation === 'save' ? 'Saving settings' : 'Sending test'}: ${message}`);
    }
  }
}
