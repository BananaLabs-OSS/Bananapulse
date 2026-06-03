import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

// AUDIT REGRESSION (XSS): the status page renders fields from the (potentially
// untrusted / compromised) upstream feed into the DOM via string concatenation
// in the client script of routes/index.astro. The fix routes every dynamic
// field through escapeHtml() and every link through safeHttpUrl() (http/https
// only — a javascript:/data: href can't execute). These tests extract the REAL
// shipped functions from the source and pin their behaviour so the escaping
// can't silently regress.

const here = dirname(fileURLToPath(import.meta.url));
const SRC = join(here, '..', 'src', 'routes', 'index.astro');
const src = readFileSync(SRC, 'utf8');

function extractFn(name: string): (...a: any[]) => any {
  // Match `function name(args) { ... }` allowing a nested brace level (the
  // safeHttpUrl try/catch). Non-greedy up to a closing brace at column-2 indent.
  const re = new RegExp(`function ${name}\\([^)]*\\) \\{[\\s\\S]*?\\n      \\}`);
  const m = src.match(re);
  if (!m) throw new Error(`${name} not found in source`);
  // eslint-disable-next-line no-new-func
  return new Function(`${m[0]}; return ${name};`)();
}

describe('Bananapulse escapeHtml (XSS regression)', () => {
  const escapeHtml = extractFn('escapeHtml');

  it('escapes &, < and >', () => {
    expect(escapeHtml('&')).toBe('&amp;');
    expect(escapeHtml('<')).toBe('&lt;');
    expect(escapeHtml('>')).toBe('&gt;');
  });

  it('neutralizes a script-injection payload (no raw angle brackets)', () => {
    const out = escapeHtml('<script>alert(1)</script>');
    expect(out).not.toContain('<');
    expect(out).not.toContain('>');
    expect(out).toBe('&lt;script&gt;alert(1)&lt;/script&gt;');
  });

  it('escapes ampersand FIRST so entities are not double-broken', () => {
    expect(escapeHtml('a & <b>')).toBe('a &amp; &lt;b&gt;');
  });

  it('coerces non-strings without throwing', () => {
    expect(escapeHtml(123)).toBe('123');
    expect(escapeHtml(null)).toBe('null');
  });
});

describe('Bananapulse safeHttpUrl (link-scheme regression)', () => {
  const safeHttpUrl = extractFn('safeHttpUrl');

  it('allows http and https URLs', () => {
    expect(safeHttpUrl('https://status.example.com/post')).toBe('https://status.example.com/post');
    expect(safeHttpUrl('http://example.com')).toBe('http://example.com');
  });

  it('REGRESSION: strips javascript: URLs to empty string', () => {
    expect(safeHttpUrl('javascript:alert(1)')).toBe('');
    expect(safeHttpUrl('JavaScript:alert(1)')).toBe('');
  });

  it('REGRESSION: strips data: URLs', () => {
    expect(safeHttpUrl('data:text/html,<script>alert(1)</script>')).toBe('');
  });

  it('rejects other schemes (vbscript, file, ftp)', () => {
    expect(safeHttpUrl('vbscript:msgbox(1)')).toBe('');
    expect(safeHttpUrl('file:///etc/passwd')).toBe('');
    expect(safeHttpUrl('ftp://example.com')).toBe('');
  });

  it('returns empty for non-string / empty / garbage input', () => {
    expect(safeHttpUrl('')).toBe('');
    expect(safeHttpUrl(null as any)).toBe('');
    expect(safeHttpUrl(undefined as any)).toBe('');
    expect(safeHttpUrl(42 as any)).toBe('');
  });
});

describe('Bananapulse source applies the escapers at every sink', () => {
  it('dynamic incident/row/banner fields flow through escapeHtml', () => {
    expect(src).toContain('escapeHtml(s.name)');
    expect(src).toContain('escapeHtml(s.message)');
    expect(src).toContain('escapeHtml(b.text)');
    // Incident body + postmortem link are escaped / scheme-checked.
    expect(src).toContain('escapeHtml(i.body)');
    expect(src).toContain('safeHttpUrl(i.postmortemUrl)');
  });
});
