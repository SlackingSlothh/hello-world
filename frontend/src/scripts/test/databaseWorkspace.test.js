import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import databaseWorkspace from '../databaseWorkspace.js';

function setup(t, respond) {
 const previousWindow = globalThis.window;
 const previousDocument = globalThis.document;
 const previousFetch = globalThis.fetch;
 const dashboard = { style: {} };
 const browser = new EventTarget();
 browser.location = new URL('https://example.com/db');
 browser.history = { pushState(_state, _title, url) { browser.location = new URL(url); } };
 globalThis.window = browser;
 globalThis.document = { documentElement: { dataset: { apiBase: 'https://api.example.com/' } }, getElementById() { return dashboard; } };
 globalThis.fetch = async (url, options) => respond(new URL(url), options);
 t.after(() => {
  globalThis.window = previousWindow;
  globalThis.document = previousDocument;
  globalThis.fetch = previousFetch;
 });
 return { workspace: databaseWorkspace(), browser, dashboard };
}

function catalogResponse(url) {
 if (url.pathname.endsWith('/schemas')) return Response.json([{ name: 'public' }]);
 if (url.pathname.endsWith('/tables')) return Response.json([{ name: '_film', type: 'TABLE' }]);
 if (url.pathname.endsWith('/rows')) return Response.json({ columns: ['id', 'title'], rows: [[1, 'Example film']] });
 if (url.pathname.endsWith('/columns')) return Response.json([{ name: 'id', type: 'integer' }, { name: 'title', type: 'text' }]);
 return Response.json(null);
}

const connection = { id: 'cinema', name: 'Cinema', driver: 'postgres' };

test('database card opens schema, table rows and metadata', async t => {
 const { workspace, browser, dashboard } = setup(t, catalogResponse);
 workspace.initWorkspace();
 const card = readFileSync(new URL('../../components/ConnectionDashboard.astro', import.meta.url), 'utf8');
 const expression = card.match(/@click="(!admin[^\n]+)"/)[1];
 new Function('admin', 'connection', 'window', expression)(false, connection, browser);
 await new Promise(setImmediate);
 assert.equal(workspace.activeConnection.id, 'cinema');
 assert.equal(dashboard.style.display, 'none');
 assert.equal(browser.location.search, '?id=cinema');
 assert.deepEqual(workspace.schemas, ['public']);
 assert.equal(workspace.selectedTable, '_film');
 assert.deepEqual(workspace.rowColumns, ['id', 'title']);
 assert.deepEqual(workspace.tableRows, [[1, 'Example film']]);
 assert.equal(workspace.tableColumns[1].type, 'text');
 browser.location = new URL('https://example.com/db');
 browser.dispatchEvent(new Event('popstate'));
 assert.equal(workspace.activeConnection, null);
 assert.equal(dashboard.style.display, '');
});

test('read query sends the backend payload and keeps array rows', async t => {
 const { workspace } = setup(t, (url, options) => {
  assert.equal(url.href, 'https://api.example.com/api/v1/connections/cinema/query');
  assert.equal(options.method, 'POST');
  assert.equal(options.credentials, 'include');
  assert.deepEqual(JSON.parse(options.body), { sql: 'SELECT 1, NULL', limit: 100 });
  return Response.json({ columns: ['value', 'empty'], rows: [[1, null]] });
 });
 workspace.activeConnection = connection;
 workspace.querySQL = 'SELECT 1, NULL';
 await workspace.runReadOnlyQuery();
 assert.deepEqual(workspace.queryResultRows, [[1, null]]);
 assert.deepEqual(workspace.queryResultColumns, ['value', 'empty']);
 assert.equal(workspace.error, '');
});

test('API errors are readable and clear previous query results', async t => {
 const { workspace } = setup(t, () => Response.json({ error: { code: 'invalid_request', message: 'Only read queries are allowed' } }, { status: 400 }));
 workspace.activeConnection = connection;
 workspace.queryResultRows = [[1]];
 await workspace.runReadOnlyQuery();
 assert.equal(workspace.error, 'Only read queries are allowed');
 assert.deepEqual(workspace.queryResultRows, []);
 assert.equal(workspace.runningQuery, false);
});

test('switching databases ignores a late response and clears old data', async t => {
 let finishOldRequest;
 const { workspace } = setup(t, url => {
  if (url.pathname === '/api/v1/connections/old/schemas') return new Promise(resolve => { finishOldRequest = resolve; });
  return Response.json(null);
 });
 const oldLoad = workspace.openWorkspace({ ...connection, id: 'old' });
 workspace.queryResultRows = [['old data']];
 await workspace.openWorkspace({ ...connection, id: 'new' });
 finishOldRequest(Response.json([{ name: 'old_schema' }]));
 await oldLoad;
 assert.equal(workspace.activeConnection.id, 'new');
 assert.equal(workspace.activeTab, 'viewer');
 assert.deepEqual(workspace.schemas, []);
 assert.deepEqual(workspace.queryResultRows, []);
 assert.equal(workspace.selectedSchema, '');
});

test('schema changes reload a table with the same name', async t => {
 const paths = [];
 const { workspace } = setup(t, url => { paths.push(url.pathname); return catalogResponse(url); });
 await workspace.openWorkspace(connection);
 workspace.selectedSchema = 'archive';
 await workspace.loadTables();
 assert.ok(paths.includes('/api/v1/connections/cinema/schemas/archive/tables/_film/rows'));
});

test('an inaccessible database link reports the error on the dashboard', async t => {
 const { workspace, browser, dashboard } = setup(t, () => new Response('access denied', { status: 403 }));
 let message;
 browser.addEventListener('datadock:workspace-error', event => { message = event.detail; });
 await workspace.restoreConnectionById('forbidden');
 assert.equal(workspace.activeConnection, null);
 assert.equal(dashboard.style.display, '');
 assert.equal(message, 'access denied');
});
