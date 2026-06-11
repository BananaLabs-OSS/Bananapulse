/**
 * Admin-session authentication for the write endpoints behind the admin
 * (`/api/v1/admin/*`). These are NOT bearer-token routes — they are reached by
 * the logged-in admin's browser session, the same magic-link cookie that
 * guards the `/admin` pages. The public ingest/summary/sweep endpoints keep
 * their own auth; this is only for the human-facing CMS write surface.
 */
import type { APIContext } from 'astro';
import { verifyCookie, validateSession, COOKIE_NAME } from './admin-auth';

/** Resolve the admin email from the session cookie, or null if unauthenticated. */
export async function adminEmailFromRequest(ctx: APIContext): Promise<string | null> {
  // Middleware sets locals.adminEmail for /admin/* but lets /api/* pass through,
  // so resolve it directly from the signed session cookie here.
  const fromLocals = (ctx.locals as App.Locals).adminEmail;
  if (fromLocals) return fromLocals;

  const signed = ctx.cookies.get(COOKIE_NAME)?.value;
  if (!signed) return null;
  const secret = process.env.ADMIN_SESSION_SECRET;
  if (!secret) return null;
  const sessionId = verifyCookie(signed, secret);
  if (!sessionId) return null;
  return validateSession(sessionId);
}

/** JSON helpers — consistent { data } / { error } envelope across admin routes. */
export function ok(data: unknown, status = 200): Response {
  return new Response(JSON.stringify({ data }), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

export function err(code: string, message: string, status: number): Response {
  return new Response(JSON.stringify({ error: { code, message } }), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

/**
 * Guard an admin API handler: returns the admin email or a 401 Response.
 * Usage: `const who = await requireAdmin(ctx); if (who instanceof Response) return who;`
 */
export async function requireAdmin(ctx: APIContext): Promise<string | Response> {
  const email = await adminEmailFromRequest(ctx);
  if (!email) return err('unauthorized', 'Admin session required.', 401);
  return email;
}
