/** Read an admin API response without exposing an HTML error page as a JSON parse error. */
export async function readAdminJSON(response: Response): Promise<any> {
  if (response.redirected) throw new Error('Your session may have expired. Sign in again and retry.');
  if (!response.headers.get('Content-Type')?.includes('application/json')) {
    throw new Error(`The server returned a page instead of an API response (HTTP ${response.status}). Reload and retry.`);
  }
  let envelope;
  try { envelope = await response.json(); }
  catch { throw new Error(`The server returned an invalid API response (HTTP ${response.status}). Retry shortly.`); }
  if (!response.ok) {
    if (response.status === 401) throw new Error('Your session has expired. Sign in again and retry.');
    throw new Error(envelope?.error?.message ?? `Request failed (HTTP ${response.status}).`);
  }
  if (!envelope || !('data' in envelope)) throw new Error('The server returned an incomplete API response. Retry shortly.');
  return envelope.data;
}
