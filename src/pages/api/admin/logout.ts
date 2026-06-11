import type { APIRoute } from 'astro';
import { destroySession, verifyCookie, COOKIE_NAME } from '@/lib/admin-auth';

export const POST: APIRoute = async ({ cookies, redirect }) => {
  const sessionCookie = cookies.get(COOKIE_NAME)?.value;
  if (sessionCookie) {
    const secret = process.env.ADMIN_SESSION_SECRET ?? '';
    const sessionId = verifyCookie(sessionCookie, secret);
    if (sessionId) {
      await destroySession(sessionId);
    }
  }
  cookies.delete(COOKIE_NAME, { path: '/' });
  return redirect('/admin/login');
};
