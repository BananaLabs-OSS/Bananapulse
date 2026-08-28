import type { APIRoute } from 'astro';
import { destroySession, destroySessionWithOwner, verifyCookie, COOKIE_NAME } from '@/lib/admin-auth';
import { pulpOwnerRouteFamilyConfigured } from '@/lib/pulp-bridge';

export const POST: APIRoute = async ({ cookies, redirect }) => {
  const sessionCookie = cookies.get(COOKIE_NAME)?.value;
  if (sessionCookie) {
    const secret = process.env.ADMIN_SESSION_SECRET ?? '';
    const sessionId = verifyCookie(sessionCookie, secret);
    if (sessionId) {
      if (pulpOwnerRouteFamilyConfigured('auth')) {
        await destroySessionWithOwner(sessionId);
      } else {
        await destroySession(sessionId);
      }
    }
  }
  cookies.delete(COOKIE_NAME, { path: '/' });
  return redirect('/admin/login');
};
