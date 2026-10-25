import test from 'node:test';
import assert from 'node:assert/strict';
import editConnectionForm from '../editConnectionForm.js';

function setup(t, respond) {
 const old = { window: globalThis.window, document: globalThis.document, fetch: globalThis.fetch };
 globalThis.window = { location: { search: '?id=db1' } };
 globalThis.document = { documentElement: { dataset: { apiBase: 'https://api.example.com/' } } };
 globalThis.fetch = respond;
 t.after(() => Object.assign(globalThis, old));
 return editConnectionForm();
}
const connection = { id:'db1', name:'Cinema', driver:'postgres', pool:{maxOpen:5, maxIdle:2, maxLifetimeSeconds:60} };

test('edit form loads safe settings and keeps a blank DSN', async t => {
 const form = setup(t, async (url, options) => {
  assert.equal(url, 'https://api.example.com/api/v1/connections/db1/settings');
  assert.equal(options.credentials, 'include');
  return Response.json(connection);
 });
 await form.init();
 assert.equal(form.loaded, true);
 assert.equal(form.name, 'Cinema');
 assert.equal(form.maxOpen, 5);
 assert.equal(form.dsn, '');
});

test('saving name and pool preserves other pool settings', async t => {
 const form = setup(t, async (url, options) => {
  if (url.endsWith('/settings')) return Response.json(connection);
  assert.equal(options.method, 'PATCH');
  assert.deepEqual(JSON.parse(options.body), { name:'Renamed', driver:'postgres', dsn:'', pool:{maxOpen:8, maxIdle:2, maxLifetimeSeconds:60} });
  return Response.json({...connection, name:'Renamed'});
 });
 await form.init();
 form.name = 'Renamed';
 form.maxOpen = 8;
 await form.submit();
 assert.equal(form.notice, 'Connection settings saved.');
 assert.equal(form.dsn, '');
});

test('engine change needs a DSN and failed saves show the API error', async t => {
 let saves = 0;
 const form = setup(t, async (url) => {
  if (url.endsWith('/settings')) return Response.json(connection);
  saves++;
  return Response.json({error:{message:'Could not verify connection'}}, {status:502});
 });
 await form.init();
 form.driver = 'mysql';
 await form.submit();
 assert.equal(saves, 0);
 assert.match(form.error, /new DSN/);
 form.dsn = 'replacement-secret';
 await form.submit();
 assert.equal(saves, 1);
 assert.equal(form.error, 'Could not verify connection');
 assert.equal(form.dsn, '');
 assert.equal(form.originalDriver, 'postgres');
});
