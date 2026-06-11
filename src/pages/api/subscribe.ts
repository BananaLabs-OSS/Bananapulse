import type { APIRoute } from 'astro';
import { addSubscriber } from '@/lib/subscribers';

/**
 * Per-IP fixed-window rate limit for this unauthenticated write endpoint.
 * In-memory only (single-instance app) — enough to stop a flood of the
 * subscribers table without pulling in a dependency. Map grows at most one
 * entry per active client IP per window; stale windows are reset lazily on hit.
 */
const RATE_LIMIT = 5; // requests per IP per window
const RATE_WINDOW_MS = 60_000; // 1 minute
const hits = new Map<string, { count: number; resetAt: number }>();

function clientIp(request: Request): string {
  const fwd = request.headers.get('x-forwarded-for');
  if (fwd) return fwd.split(',')[0].trim();
  return request.headers.get('x-real-ip')?.trim() || 'unknown';
}

/** Returns true if the IP is over its window budget. */
function rateLimited(ip: string): boolean {
  const now = Date.now();
  const entry = hits.get(ip);
  if (!entry || now >= entry.resetAt) {
    hits.set(ip, { count: 1, resetAt: now + RATE_WINDOW_MS });
    return false;
  }
  entry.count += 1;
  return entry.count > RATE_LIMIT;
}

/**
 * POST /api/subscribe — persist an email subscriber.
 * Accepts { email }. Idempotent on the unique email; always acks with {ok:true}
 * for a valid email (anti-enumeration — never reveals whether it already existed).
 */
export const POST: APIRoute = async ({ request }) => {
  if (rateLimited(clientIp(request))) {
    return new Response(
      JSON.stringify({ ok: false, error: 'Too many requests. Please try again shortly.' }),
      { status: 429, headers: { 'Content-Type': 'application/json' } },
    );
  }

  try {
    const body = await request.json();
    const email = typeof body?.email === 'string' ? body.email.toLowerCase().trim() : '';

    const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
    if (!email || !emailRegex.test(email)) {
      return new Response(
        JSON.stringify({ ok: false, error: 'A valid email address is required.' }),
        { status: 400, headers: { 'Content-Type': 'application/json' } },
      );
    }

    try {
      await addSubscriber(email);
    } catch (_e) {
      // Duplicate (already subscribed) or transient — still ack to avoid leaking state.
    }

    return new Response(
      JSON.stringify({ ok: true }),
      { status: 200, headers: { 'Content-Type': 'application/json' } },
    );
  } catch {
    return new Response(
      JSON.stringify({ ok: false, error: 'Invalid request body.' }),
      { status: 400, headers: { 'Content-Type': 'application/json' } },
    );
  }
};
