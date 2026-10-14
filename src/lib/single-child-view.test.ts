import { expect, it } from 'vitest';
import type { ScopeView } from './view';
import { singleChildDestination } from './single-child-view';
const view = (...paths: string[]) => ({ children: paths.map(href => ({href})) }) as ScopeView;
it('skips a sole product and sole server in one redirect', async () => {
 const views: Record<string,ScopeView> = {'product':view('/product/server'),'product/server':view()};
 expect(await singleChildDestination(view('/product'),'/',segments=>views[segments.join('/')])).toBe('/product/server');
});
it('keeps a choice between siblings and leaves leaf URLs alone', async () => {
 expect(await singleChildDestination(view('/a','/b'),'/',()=>null)).toBeNull();
 expect(await singleChildDestination(view(),'/product/server',()=>null)).toBeNull();
 expect(await singleChildDestination(view('/product'), '/',()=>view('/product/a','/product/b'))).toBe('/product');
});
it('cannot redirect into missing nodes, cycles or external locations', async () => {
 expect(await singleChildDestination(view('/missing'),'/',()=>null)).toBeNull();
 expect(await singleChildDestination(view('//outside.example'),'/',()=>view())).toBeNull();
 expect(await singleChildDestination(view('/a'),'/',()=>view('/'))).toBe('/a');
 expect(await singleChildDestination(view('/a/'),'/a/',()=>view())).toBeNull();
});
