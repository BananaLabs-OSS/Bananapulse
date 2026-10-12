import type { APIRoute } from 'astro';
import { requireAdmin, ok, err } from '@/lib/admin-api';
import { callPulpEvent, pulpBridgeConfigured } from '@/lib/pulp-bridge';

const SETTINGS = 'bananapulse.host.discord.settings.v1';
const TEST = 'bananapulse.host.discord.test.v1';
function privateResponse(response: Response): Response {
  response.headers.set('Cache-Control', 'no-store');
  return response;
}
export const GET: APIRoute = async (ctx) => {
  let who;
  try { who = await requireAdmin(ctx); }
  catch { return privateResponse(err('unavailable', 'Sign-in verification is unavailable. Retry shortly.', 503)); }
  if (who instanceof Response) return privateResponse(who);
  if (!pulpBridgeConfigured()) return privateResponse(err('unavailable', 'Discord notifications require the Pulp notification host.', 503));
  try { return privateResponse(ok(await callPulpEvent(SETTINGS, { action: 'get' }))); }
  catch { return privateResponse(err('unavailable', 'Discord settings are unavailable. Check that the host is updated and admin notifications are enabled.', 503)); }
};
export const POST: APIRoute = async (ctx) => {
  let who;
  try { who = await requireAdmin(ctx); }
  catch { return privateResponse(err('unavailable', 'Sign-in verification is unavailable. Retry shortly.', 503)); }
  if (who instanceof Response) return privateResponse(who);
  // Session-authenticated mutations must originate from this admin site.
  if (ctx.request.headers.get('Origin') !== ctx.url.origin) return privateResponse(err('forbidden', 'Same-origin request required.', 403));
  if (!ctx.request.headers.get('Content-Type')?.startsWith('application/json')) return privateResponse(err('bad_request', 'JSON required.', 400));
  if (!pulpBridgeConfigured()) return privateResponse(err('unavailable', 'Discord notifications require the Pulp notification host.', 503));
  const body = await ctx.request.json().catch(() => null);
  if (!body || !['save', 'test'].includes(body.action)) return privateResponse(err('bad_request', 'Choose save or test.', 400));
  if (body.action === 'save' && (
    typeof body.webhookUrl !== 'string' || body.webhookUrl.length > 512 ||
    !['enabled', 'outages', 'recoveries', 'remove'].every((key) => typeof body[key] === 'boolean')
  )) return privateResponse(err('bad_request', 'Invalid notification settings.', 400));
  try {
    const request = body.action === 'test' ? {} : {
      action: 'save', webhookUrl: body.webhookUrl.trim(), enabled: body.enabled,
      outages: body.outages, recoveries: body.recoveries, remove: body.remove,
    };
    return privateResponse(ok(await callPulpEvent(body.action === 'test' ? TEST : SETTINGS, request)));
  } catch {
    return privateResponse(err('discord_failed', body.action === 'test'
      ? 'Test failed. Check the saved webhook and try again.'
      : 'Could not save. Use a Discord HTTPS webhook URL and check host availability.', 502));
  }
};
