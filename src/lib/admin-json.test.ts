import { expect, it } from 'vitest';
import { readAdminJSON } from './admin-json';
it('reports HTML gateway failures without parsing or exposing their contents', async () => {
  await expect(readAdminJSON(new Response('<!DOCTYPE html>private edge page', { status: 502, headers: { 'Content-Type': 'text/html' } }))).rejects.toThrow('HTTP 502');
});
it('reports expired sessions and API errors', async () => {
  await expect(readAdminJSON(Response.json({ error: { message: 'Admin session required.' } }, { status: 401 }))).rejects.toThrow('Sign in again');
  await expect(readAdminJSON(Response.json({ error: { message: 'Host unavailable.' } }, { status: 503 }))).rejects.toThrow('Host unavailable.');
});
it('accepts valid data and rejects malformed JSON', async () => {
  expect(await readAdminJSON(Response.json({ data: { configured: false } }))).toEqual({ configured: false });
  await expect(readAdminJSON(new Response('{', { headers: { 'Content-Type': 'application/json' } }))).rejects.toThrow('invalid API response');
});
