/** JSON data embedded in an HTML script element must not contain a closing tag. */
export function publicJSON(value: unknown): string {
  return JSON.stringify(value).replace(/</g, '\\u003c');
}
