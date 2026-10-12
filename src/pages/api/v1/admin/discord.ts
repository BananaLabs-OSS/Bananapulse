import type { APIRoute } from 'astro';
import { requireAdmin, ok, err } from '@/lib/admin-api';
import { callPulpEvent, pulpBridgeConfigured, PulpBridgeError } from '@/lib/pulp-bridge';

const SETTINGS = 'bananapulse.host.discord.settings.v1';
const TEST = 'bananapulse.host.discord.test.v1';
// Accept copied Discord URLs on the legacy hostname; the host still validates
// the full canonical URL before storing it or making any outbound request.
function canonicalWebhook(value: string): string {
  const trimmed = value.trim();
  try {
    const url = new URL(trimmed);
    if (url.protocol === 'https:' && url.host === 'discordapp.com' && !url.username && !url.password) {
      url.hostname = 'discord.com';
      return url.toString();
    }
  } catch {}
  return trimmed;
}
function privateResponse(response: Response): Response {
  response.headers.set('Cache-Control', 'no-store');
  return response;
}
const get: APIRoute = async (ctx) => {
  let who;
  try { who = await requireAdmin(ctx); }
  catch { return privateResponse(err('unavailable', 'Sign-in verification is unavailable. Retry shortly.', 503)); }
  if (who instanceof Response) return privateResponse(who);
  if (!pulpBridgeConfigured()) return privateResponse(err('unavailable', 'Discord notifications require the Pulp notification host.', 503));
  try { return privateResponse(ok(await callPulpEvent(SETTINGS, { action: 'get' }))); }
  catch { return privateResponse(err('unavailable', 'Discord settings are unavailable. Check that the host is updated and admin notifications are enabled.', 503)); }
};
const post: APIRoute = async (ctx) => {
  let who;
  try { who = await requireAdmin(ctx); }
  catch { return privateResponse(err('unavailable', 'Sign-in verification is unavailable. Retry shortly.', 503)); }
  if (who instanceof Response) return privateResponse(who);
  // Session-authenticated mutations must originate from this admin site.
  if (ctx.request.headers.get('Origin') !== ctx.url.origin) return privateResponse(err('forbidden', 'Same-origin request required.', 403));
  if (!ctx.request.headers.get('Content-Type')?.startsWith('application/json')) return privateResponse(err('bad_request', 'JSON required.', 400));
  if (!pulpBridgeConfigured()) return privateResponse(err('unavailable', 'Discord notifications require the Pulp notification host.', 503));
  const body = await ctx.request.json().catch(() => null);
  if (body?.action === 'diagnostic') {
    if (!['load', 'save', 'test'].includes(body.operation) || !Number.isInteger(body.status) || body.status < 0 || body.status > 599 ||
        typeof body.requestId !== 'string' || !/^[a-f0-9-]{1,36}$/.test(body.requestId) ||
        typeof body.ray !== 'string' || !/^[a-zA-Z0-9-]{0,64}$/.test(body.ray)) {
      return privateResponse(err('bad_request', 'Invalid diagnostic.', 400));
    }
    console.info(JSON.stringify({ event: 'discord.browser.failure', operation: body.operation, status: body.status, requestId: body.requestId, ray: body.ray }));
    return privateResponse(ok({ recorded: true }));
  }
  if (!body || !['save', 'test'].includes(body.action)) return privateResponse(err('bad_request', 'Choose save or test.', 400));
  if (body.action === 'save' && (
    typeof body.webhookUrl !== 'string' || body.webhookUrl.length > 512 ||
    !['enabled', 'outages', 'recoveries', 'remove'].every((key) => typeof body[key] === 'boolean')
  )) return privateResponse(err('bad_request', 'Invalid notification settings.', 400));
  try {
    const request = body.action === 'test' ? {} : {
      action: 'save', webhookUrl: canonicalWebhook(body.webhookUrl), enabled: body.enabled,
      outages: body.outages, recoveries: body.recoveries, remove: body.remove,
    };
    return privateResponse(ok(await callPulpEvent(body.action === 'test' ? TEST : SETTINGS, request)));
  } catch (error) {
    if (error instanceof PulpBridgeError && error.status === 400) {
      let reason = '';
      try { reason = JSON.parse(error.detail).error; } catch {}
      const messages: Record<string, string> = {
        'use a Discord HTTPS webhook URL from discord.com': 'Paste the full URL from Discord: Channel Settings → Integrations → Webhooks → Copy Webhook URL.',
        'save a webhook before enabling notifications': 'Paste a Discord webhook URL before enabling alerts.',
      };
      const message = messages[reason] ?? 'The notification host rejected these settings. Check the webhook URL and try again.';
      console.info(JSON.stringify({ event: 'discord.save.rejected', upstreamStatus: 400, reason: reason in messages ? reason : 'validation' }));
      return privateResponse(err('bad_request', message, 400));
    }
    console.info(JSON.stringify({ event: 'discord.host.failure', upstreamStatus: error instanceof PulpBridgeError ? error.status : 0 }));
    return privateResponse(err('discord_failed', body.action === 'test'
      ? 'Test failed. Check the saved webhook and try again.'
      : 'Could not save. Use a Discord HTTPS webhook URL and check host availability.', 502));
  }
};

function traced(handler: APIRoute): APIRoute {
  return async (ctx) => {
    const supplied = ctx.request.headers.get('X-Pulse-Request-ID') ?? '';
    const requestId = /^[a-f0-9-]{1,36}$/.test(supplied) ? supplied : 'unavailable';
    const started = Date.now();
    const response = await handler(ctx);
    console.info(JSON.stringify({ event: 'discord.api.response', method: ctx.request.method, status: response.status, requestId, elapsedMs: Date.now() - started }));
    return response;
  };
}
export const GET = traced(get);
export const POST = traced(post);
