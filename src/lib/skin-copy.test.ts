import { expect, it } from 'vitest';
import { statusWordFor, statusColorFor } from './skin-copy';
it('keeps reachable but degraded service status distinct from up and down',()=>{
 expect(statusWordFor('service','operational')).toBe('Up');
 expect(statusWordFor('service','degraded')).toBe('Degraded');
 expect(statusWordFor('service','outage')).toBe('Down');
 expect(statusColorFor('service','degraded')).toBe('var(--degr)');
});
