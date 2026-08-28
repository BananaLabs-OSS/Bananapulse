import { defineMiddleware } from 'astro:middleware';
import { resolveScope } from './lib/scope';
import { verifyCookie, COOKIE_NAME, validateSessionWithOwner } from './lib/admin-auth';
import { pulpOwnerRouteFamilyConfigured } from './lib/pulp-bridge';
import { SCOPES, UMBRELLA_ID } from './pulse.config';

export const onRequest = defineMiddleware(async ({ request, locals, url, cookies, redirect }, next) => {
  const host = request.headers.get('host') || 'localhost';
  let scope = resolveScope(host);

  // DEV-ONLY: preview any configured brand locally via ?scope=<scope id>
  // (real deploys pick the brand from the Host header). Persisted in a cookie so
  // it survives the internal rewrite that re-runs this middleware. import.meta.env
  // .DEV is false in production builds, so this is dead code there.
  if (import.meta.env.DEV) {
    const s = url.searchParams.get('scope');
    if (s === UMBRELLA_ID) cookies.delete('__scope', { path: '/' });
    else if (s) cookies.set('__scope', s, { path: '/', httpOnly: false });
    const ov = s ?? cookies.get('__scope')?.value ?? null;
    if (ov === UMBRELLA_ID) scope = null;
    else if (ov && SCOPES.some((sc) => sc.id === ov)) scope = ov;
  }

  (locals as any).scope = scope;

  const path = url.pathname;

  // Admin auth — protect /admin/* except login and verify
  if (path.startsWith('/admin') && path !== '/admin/login' && path !== '/admin/verify') {
    const sessionCookie = cookies.get(COOKIE_NAME)?.value;

    if (!sessionCookie) {
      return redirect('/admin/login');
    }

    const secret = process.env.ADMIN_SESSION_SECRET;
    if (!secret) {
      return redirect('/admin/login');
    }

    const sessionId = verifyCookie(sessionCookie, secret);
    if (!sessionId) {
      return redirect('/admin/login');
    }

    // Once enabled, validation is exclusively owned by pulp-auth. A bridge or
    // owner error is deliberately not allowed to fall through to legacy DB
    // validation, because that would re-authorize a revoked owner session.
    const email = pulpOwnerRouteFamilyConfigured('auth')
      ? await validateSessionWithOwner(sessionId)
      : await (await import('./lib/admin-auth')).validateSession(sessionId);
    if (!email) {
      cookies.delete(COOKIE_NAME, { path: '/' });
      return redirect('/admin/login');
    }

    (locals as any).adminEmail = email;
    return next();
  }

  // Node routing is relative to the scope's landing root (resolved in
  // lib/components from locals.scope), so no host-based path rewrite is needed.
  return next();
});
