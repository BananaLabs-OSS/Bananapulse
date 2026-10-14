import { expect, it } from 'vitest';
import { publicJSON } from './public-json';
it('preserves incident text without allowing it to end an embedded script',()=>{
 const data=[{title:'</script><script>alert("x")</script>',id:'auto/service/123'}];
 const encoded=publicJSON(data);
 expect(encoded).not.toContain('<');expect(JSON.parse(encoded)).toEqual(data);
});
