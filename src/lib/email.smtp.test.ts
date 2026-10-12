import { afterEach, expect, it, vi } from 'vitest';
const bridge = vi.hoisted(() => ({ call: vi.fn() }));
vi.mock('./pulp-bridge', () => ({ callPulpEvent: bridge.call }));
import { sendMagicLinkEmail } from './email';
afterEach(() => { vi.unstubAllEnvs(); vi.unstubAllGlobals(); vi.clearAllMocks(); });
it('routes SMTP login mail through the private host without calling Resend', async () => {
 vi.stubEnv('EMAIL_TRANSPORT', 'smtp');const fetch = vi.fn();vi.stubGlobal('fetch', fetch);bridge.call.mockResolvedValue({ accepted: true });
 await sendMagicLinkEmail('admin@example.test', 'https://status.example.test/admin/verify?token=private');
 expect(bridge.call).toHaveBeenCalledWith('bananapulse.host.email.smtp.send.v1', expect.objectContaining({ to:'admin@example.test', subject:'Your admin login link', text: expect.stringContaining('token=private') }));expect(fetch).not.toHaveBeenCalled();
});
it('does not report success if the internal relay rejects mail', async () => {
 vi.stubEnv('EMAIL_TRANSPORT','smtp');bridge.call.mockResolvedValue({ accepted:false });
 await expect(sendMagicLinkEmail('admin@example.test','https://status.example.test')).rejects.toThrow('did not accept');
});
