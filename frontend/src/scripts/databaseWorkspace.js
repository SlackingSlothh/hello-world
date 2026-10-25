export default function databaseWorkspace() {
	return {
		activeConnection: null,
		requests: { schemas: 0, tables: 0, rows: 0, metadata: 0, query: 0, migrations: 0, restore: 0 },
		activeTab: 'viewer',
		error: '',
		notice: '',
		testingPing: false,

		// Viewer state
		schemas: [],
		selectedSchema: 'public',
		tables: [],
		tableFilter: '',
		loadingTables: false,
		selectedTable: '',
		viewerSubTab: 'rows',
		loadingTableData: false,
		tableRows: [],
		rowColumns: [],
		rowsLimit: 50,
		rowsOffset: 0,
		rowsSortColumn: '',
		rowsSortDir: 'asc',
		tableColumns: [],
		tableConstraints: [],
		tableIndexes: [],

		// Read-only Query state
		querySQL: 'SELECT table_schema, table_name FROM information_schema.tables WHERE table_schema NOT IN (\'pg_catalog\', \'information_schema\') ORDER BY 1, 2 LIMIT 50;',
		runningQuery: false,
		queryResultColumns: [],
		queryResultRows: [],

		// Migrations state
		migrations: [],
		migrationStatusFilter: 'ALL',
		loadingMigrations: false,
		selectedMigration: null,
		migrationRuns: [],
		savingMigration: false,
		executingMigration: false,
		migrationForm: {
			title: '',
			description: '',
			sql_content: ''
		},

		// Explain state
		explainSQL: 'SELECT * FROM information_schema.tables LIMIT 25;',
		explainAnalyze: true,
		runningExplain: false,
		explainResult: null,

		// Schema Diff state
		diffSchemaName: 'public',
		runningSchemaDiff: false,
		schemaDiffResult: null,
		diffDesiredTables: [
			{
				name: 'app_Release_notes',
				columns: [
					{ name: 'id', data_type: 'BIGINT', is_nullable: false, default_value: '', is_primary_key: true },
					{ name: 'version_tag', data_type: 'TEXT', is_nullable: false, default_value: '', is_primary_key: false },
					{ name: 'published_at', data_type: 'TIMESTAMPTZ', is_nullable: false, default_value: 'NOW()', is_primary_key: false }
				]
			}
		],

		get apiBase() {
			return (document.documentElement.dataset.apiBase || '').replace(/\/+$/, '');
		},

		get filteredTables() {
			const q = this.tableFilter.trim().toLowerCase();
			if (!q) return this.tables;
			return this.tables.filter((t) => (t.name || '').toLowerCase().includes(q));
		},

		get filteredMigrations() {
			if (this.migrationStatusFilter === 'ALL') return this.migrations;
			return this.migrations.filter((m) => m.status === this.migrationStatusFilter);
		},

		get sqlGuardrailWarning() {
			const raw = (this.migrationForm.sql_content || '').toUpperCase();
			if (/\b(BEGIN|COMMIT|ROLLBACK|START\s+TRANSACTION)\b/.test(raw)) {
				return 'Guardrail warning: Avoid manual BEGIN, COMMIT, or ROLLBACK statements. The migration service automatically wraps your script in an atomic transaction.';
			}
			if (/\b(CREATE\s+DATABASE|DROP\s+DATABASE)\b/.test(raw)) {
				return 'Guardrail warning: CREATE DATABASE and DROP DATABASE cannot run inside a PostgreSQL transaction block.';
			}
			return '';
		},

		get flattenedExplainNodes() {
			if (!this.explainResult || !this.explainResult.root) return [];
			const out = [];
			const walk = (node, depth) => {
				if (!node) return;
				out.push({ node, depth });
				if (Array.isArray(node.plans)) {
					node.plans.forEach((child) => walk(child, depth + 1));
				}
			};
			walk(this.explainResult.root, 0);
			return out;
		},

		initWorkspace() {
			window.addEventListener('datadock:select-db', (e) => {
				if (e.detail) {
					this.openWorkspace(e.detail);
				}
			});

			window.addEventListener('popstate', () => this.syncLocation());
			this.syncLocation();
		},

		syncLocation() {
			const params = new URLSearchParams(window.location.search);
			const connId = params.get('id') || params.get('connection');
			if (connId) {
				this.restoreConnectionById(connId);
			} else {
				this.closeWorkspace(false);
			}
		},

		async apiFetch(path, options = {}) {
			const res = await fetch(`${this.apiBase}${path}`, {
				credentials: 'include',
				headers: {
					'Content-Type': 'application/json',
					...(options.headers || {})
				},
				...options
			});
			const contentType = res.headers.get('content-type') || '';
			const payload = contentType.includes('application/json') ? await res.json().catch(() => null) : await res.text().catch(() => '');
			if (!res.ok) {
				const msg =
					(payload && (payload.error?.message || payload.message || (typeof payload.error === 'string' && payload.error))) ||
					(typeof payload === 'string' && payload) ||
					`Request failed (${res.status})`;
				throw new Error(msg);
			}
			return payload;
		},

		toggleDashboardVisibility(showDashboard) {
			const dash = document.getElementById('datadock-connection-dashboard');
			if (dash) {
				dash.style.display = showDashboard ? '' : 'none';
			}
		},

		async restoreConnectionById(id) {
			const request = ++this.requests.restore;
			try {
				const data = await this.apiFetch(`/api/v1/connections/${encodeURIComponent(id)}`);
				if (request !== this.requests.restore) return;
				const conn = data?.connection || data;
				if (conn && conn.id) {
					this.openWorkspace(conn, false);
				}
			} catch (error) {
				if (request !== this.requests.restore) return;
				// Fallback if single-connection endpoint fails: try listing connections
				try {
					const listData = await this.apiFetch('/api/v1/connections');
					if (request !== this.requests.restore) return;
					const list = Array.isArray(listData) ? listData : listData?.connections || [];
					const match = list.find((c) => String(c.id) === String(id));
					if (match) { this.openWorkspace(match, false); return; }
				} catch (_) {}
				if (request !== this.requests.restore) return;
				this.closeWorkspace(false);
				window.dispatchEvent(new CustomEvent('datadock:workspace-error', { detail: error.message }));
			}
		},

		openWorkspace(connection, pushUrl = true) {
			this.resetViewer();
			this.activeConnection = connection;
			this.activeTab = 'viewer';
			this.error = '';
			this.notice = '';
			this.selectedTable = '';
			this.selectedMigration = null;
			this.migrationRuns = [];
			this.explainResult = null;
			this.schemaDiffResult = null;
			this.toggleDashboardVisibility(false);

			if (pushUrl) {
				const url = new URL(window.location.href);
				url.searchParams.set('id', connection.id);
				window.history.pushState({}, '', url.toString());
			}

			return Promise.all([this.loadSchemas(), this.loadMigrations()]);
		},

		closeWorkspace(pushUrl = true) {
			this.resetViewer();
			this.activeConnection = null;
			this.error = '';
			this.notice = '';
			this.toggleDashboardVisibility(true);
			if (!pushUrl) return;
			const url = new URL(window.location.href);
			url.searchParams.delete('id');
			url.searchParams.delete('connection');
			window.history.pushState({}, '', url.toString());
		},

		resetTable() {
			this.requests.rows++;
			this.requests.metadata++;
			this.selectedTable = '';
			this.tableRows = [];
			this.rowColumns = [];
			this.tableColumns = [];
			this.tableConstraints = [];
			this.tableIndexes = [];
			this.loadingTableData = false;
		},

		resetViewer() {
			for (const key of Object.keys(this.requests)) this.requests[key]++;
			this.resetTable();
			this.schemas = [];
			this.tables = [];
			this.selectedSchema = '';
			this.tableFilter = '';
			this.queryResultRows = [];
			this.queryResultColumns = [];
			this.migrations = [];
			this.loadingTables = false;
			this.loadingMigrations = false;
			this.runningQuery = false;
		},

		switchTab(tab) {
			this.activeTab = tab;
			this.error = '';
			if (tab === 'viewer' && this.schemas.length === 0) {
				this.loadSchemas();
			}
			if (tab === 'migrations') {
				this.loadMigrations();
			}
		},

		refreshActiveTab() {
			if (this.activeTab === 'viewer') {
				this.loadSchemas();
			} else if (this.activeTab === 'migrations') {
				this.loadMigrations();
			}
		},

		async pingConnection() {
			if (!this.activeConnection) return;
			this.testingPing = true;
			this.error = '';
			this.notice = '';
			try {
				const res = await this.apiFetch(`/api/v1/connections/${encodeURIComponent(this.activeConnection.id)}/test`, {
					method: 'POST'
				});
				this.activeConnection.reachable = true;
				const latency = res?.latencyMs !== undefined ? ` (${res.latencyMs} ms)` : '';
				this.notice = `Connection "${this.activeConnection.name}" is reachable${latency}.`;
			} catch (err) {
				this.activeConnection.reachable = false;
				this.error = err.message;
			} finally {
				this.testingPing = false;
			}
		},

		// ==================== VIEWER METHODS ====================
		async loadSchemas() {
			if (!this.activeConnection) return;
			const request = ++this.requests.schemas;
			this.requests.tables++;
			this.loadingTables = false;
			this.error = '';
			this.schemas = [];
			this.tables = [];
			this.resetTable();
			try {
				const data = await this.apiFetch(`/api/v1/connections/${encodeURIComponent(this.activeConnection.id)}/schemas`);
				if (request !== this.requests.schemas) return;
				this.schemas = (data || []).map(schema => schema.name);
				if (!this.schemas.includes(this.selectedSchema)) {
					this.selectedSchema = this.schemas.includes('public') ? 'public' : this.schemas[0] || '';
				}
				if (this.selectedSchema) await this.loadTables();
			} catch (err) {
				if (request === this.requests.schemas) this.error = err.message;
			}
		},

		async loadTables() {
			const request = ++this.requests.tables;
			const previousTable = this.selectedTable;
			this.resetTable();
			this.tables = [];
			this.error = '';
			if (!this.activeConnection || !this.selectedSchema) return;
			this.loadingTables = true;
			try {
				const tables = await this.apiFetch(`/api/v1/connections/${encodeURIComponent(this.activeConnection.id)}/schemas/${encodeURIComponent(this.selectedSchema)}/tables`);
				if (request !== this.requests.tables) return;
				this.tables = tables || [];
				const table = this.tables.find(table => table.name === previousTable) || this.tables[0];
				if (table) await this.selectTable(table.name);
			} catch (err) {
				if (request === this.requests.tables) this.error = err.message;
			} finally {
				if (request === this.requests.tables) this.loadingTables = false;
			}
		},

		async selectTable(tableName) {
			this.resetTable();
			this.error = '';
			this.selectedTable = tableName;
			this.rowsOffset = 0;
			this.rowsSortColumn = '';
			await Promise.all([
				this.loadTableRows(),
				this.loadTableMetadata()
			]);
		},

		switchViewerSubTab(subTab) {
			this.viewerSubTab = subTab;
		},

		async loadTableRows() {
			if (!this.activeConnection || !this.selectedSchema || !this.selectedTable) return;
			const request = ++this.requests.rows;
			this.tableRows = [];
			this.rowColumns = [];
			this.error = '';
			this.loadingTableData = true;
			try {
				const params = new URLSearchParams({
					limit: String(this.rowsLimit),
					offset: String(this.rowsOffset)
				});
				if (this.rowsSortColumn) {
					params.set('sort', this.rowsSortColumn);
					params.set('direction', this.rowsSortDir);
				}
				const data = await this.apiFetch(
					`/api/v1/connections/${encodeURIComponent(this.activeConnection.id)}/schemas/${encodeURIComponent(this.selectedSchema)}/tables/${encodeURIComponent(this.selectedTable)}/rows?${params.toString()}`
				);
				if (request !== this.requests.rows) return;
				this.tableRows = data.rows || [];
				this.rowColumns = data.columns || [];
			} catch (err) {
				if (request === this.requests.rows) this.error = err.message;
			} finally {
				if (request === this.requests.rows) this.loadingTableData = false;
			}
		},

		async loadTableMetadata() {
			if (!this.activeConnection || !this.selectedSchema || !this.selectedTable) return;
			const request = ++this.requests.metadata;
			const base = `/api/v1/connections/${encodeURIComponent(this.activeConnection.id)}/schemas/${encodeURIComponent(this.selectedSchema)}/tables/${encodeURIComponent(this.selectedTable)}`;
			try {
				const [columns, constraints, indexes] = await Promise.all([
					this.apiFetch(`${base}/columns`), this.apiFetch(`${base}/constraints`), this.apiFetch(`${base}/indexes`)
				]);
				if (request !== this.requests.metadata) return;
				this.tableColumns = columns || [];
				this.tableConstraints = constraints || [];
				this.tableIndexes = indexes || [];
			} catch (err) {
				if (request === this.requests.metadata) this.error = err.message;
			}
		},

		prevRowsPage() {
			this.rowsOffset = Math.max(0, this.rowsOffset - this.rowsLimit);
			this.loadTableRows();
		},

		nextRowsPage() {
			this.rowsOffset += this.rowsLimit;
			this.loadTableRows();
		},

		openQueryForSelectedTable() {
			if (!this.selectedTable) return;
			const quote = this.activeConnection.driver === 'postgres' ? '"' : '`';
			const identifier = value => quote + value.replaceAll(quote, quote + quote) + quote;
			this.querySQL = `SELECT * FROM ${identifier(this.selectedSchema)}.${identifier(this.selectedTable)} LIMIT 50;`;
			this.activeTab = 'query';
			this.runReadOnlyQuery();
		},

		// ==================== READ-ONLY QUERY METHODS ====================
		async runReadOnlyQuery() {
			if (!this.activeConnection || !this.querySQL.trim()) return;
			const request = ++this.requests.query;
			this.queryResultRows = [];
			this.queryResultColumns = [];
			this.runningQuery = true;
			this.error = '';
			try {
				const data = await this.apiFetch(`/api/v1/connections/${encodeURIComponent(this.activeConnection.id)}/query`, {
					method: 'POST',
					body: JSON.stringify({
						sql: this.querySQL,
						limit: 100
					})
				});
				if (request !== this.requests.query) return;
				this.queryResultRows = data.rows || [];
				this.queryResultColumns = data.columns || [];
			} catch (err) {
				if (request === this.requests.query) this.error = err.message;
			} finally {
				if (request === this.requests.query) this.runningQuery = false;
			}
		},

		sendQueryToExplain() {
			this.explainSQL = this.querySQL;
			this.activeTab = 'explain';
			this.runExplainPlan();
		},

		// ==================== MIGRATION METHODS ====================
		async loadMigrations() {
			if (!this.activeConnection) return;
			const request = ++this.requests.migrations;
			this.loadingMigrations = true;
			try {
				const data = await this.apiFetch(`/api/v1/connections/${encodeURIComponent(this.activeConnection.id)}/migrations`);
				if (request !== this.requests.migrations) return;
				this.migrations = Array.isArray(data) ? data : data?.migrations || [];
				if (this.selectedMigration) {
					const refreshed = this.migrations.find((m) => m.id === this.selectedMigration.id);
					if (refreshed) {
						await this.selectMigration(refreshed.id);
					}
				}
			} catch (err) {
				// Do not block workspace if migration routes are not yet mounted on a non-postgres db
				if (request === this.requests.migrations) this.migrations = [];
			} finally {
				if (request === this.requests.migrations) this.loadingMigrations = false;
			}
		},

		startNewMigrationDraft() {
			this.selectedMigration = null;
			this.migrationRuns = [];
			this.migrationForm = {
				title: '',
				description: '',
				sql_content: ''
			};
			this.error = '';
		},

		async selectMigration(migrationID) {
			if (!this.activeConnection) return;
			this.error = '';
			try {
				const data = await this.apiFetch(
					`/api/v1/connections/${encodeURIComponent(this.activeConnection.id)}/migrations/${encodeURIComponent(migrationID)}`
				);
				const mig = data?.migration || data;
				this.selectedMigration = mig;
				this.migrationRuns = data?.runs || mig?.runs || [];
				this.migrationForm = {
					title: mig?.title || '',
					description: mig?.description || '',
					sql_content: mig?.sql_content || ''
				};
			} catch (err) {
				this.error = err.message;
			}
		},

		async createMigrationDraft() {
			if (!this.activeConnection) return;
			this.savingMigration = true;
			this.error = '';
			this.notice = '';
			try {
				const created = await this.apiFetch(`/api/v1/connections/${encodeURIComponent(this.activeConnection.id)}/migrations`, {
					method: 'POST',
					body: JSON.stringify(this.migrationForm)
				});
				const mig = created?.migration || created;
				this.notice = `Draft migration "${mig.title}" (#${mig.id}) saved.`;
				await this.loadMigrations();
				if (mig?.id) {
					await this.selectMigration(mig.id);
				}
			} catch (err) {
				this.error = err.message;
			} finally {
				this.savingMigration = false;
			}
		},

		async updateMigrationDraft() {
			if (!this.activeConnection || !this.selectedMigration) return;
			this.savingMigration = true;
			this.error = '';
			this.notice = '';
			try {
				const updated = await this.apiFetch(
					`/api/v1/connections/${encodeURIComponent(this.activeConnection.id)}/migrations/${encodeURIComponent(this.selectedMigration.id)}`,
					{
						method: 'PATCH',
						body: JSON.stringify(this.migrationForm)
					}
				);
				const mig = updated?.migration || updated;
				this.selectedMigration = mig;
				this.notice = `Migration #${mig.id} updated.`;
				await this.loadMigrations();
			} catch (err) {
				this.error = err.message;
			} finally {
				this.savingMigration = false;
			}
		},

		async executeSelectedMigration() {
			if (!this.activeConnection || !this.selectedMigration) return;
			this.executingMigration = true;
			this.error = '';
			this.notice = '';
			try {
				const res = await this.apiFetch(
					`/api/v1/connections/${encodeURIComponent(this.activeConnection.id)}/migrations/${encodeURIComponent(this.selectedMigration.id)}/execute`,
					{ method: 'POST' }
				);
				const mig = res?.migration || res;
				this.notice = `Migration "${mig.title}" executed atomically and committed as version v${mig.version}!`;
				await this.loadMigrations();
				await this.selectMigration(this.selectedMigration.id);
				await this.loadSchemas();
			} catch (err) {
				this.error = `Transaction rolled back automatically: ${err.message}`;
				await this.loadMigrations();
				await this.selectMigration(this.selectedMigration.id);
			} finally {
				this.executingMigration = false;
			}
		},

		sendMigrationSQLToExplain() {
			this.explainSQL = this.migrationForm.sql_content;
			this.activeTab = 'explain';
			this.runExplainPlan();
		},

		// ==================== EXPLAIN PLAN METHODS ====================
		async runExplainPlan() {
			if (!this.activeConnection || !this.explainSQL.trim()) return;
			this.runningExplain = true;
			this.error = '';
			try {
				const res = await this.apiFetch(`/api/v1/connections/${encodeURIComponent(this.activeConnection.id)}/migrations/explain`, {
					method: 'POST',
					body: JSON.stringify({
						sql_content: this.explainSQL,
						analyze: this.explainAnalyze
					})
				});
				this.explainResult = res;
			} catch (err) {
				this.error = err.message;
			} finally {
				this.runningExplain = false;
			}
		},

		// ==================== SCHEMA DIFF METHODS ====================
		addDiffTable() {
			this.diffDesiredTables.push({
				name: `new_table_${this.diffDesiredTables.length + 1}`,
				columns: [
					{ name: 'id', data_type: 'BIGINT', is_nullable: false, default_value: '', is_primary_key: true }
				]
			});
		},

		removeDiffTable(tIdx) {
			this.diffDesiredTables.splice(tIdx, 1);
		},

		addDiffColumn(tIdx) {
			this.diffDesiredTables[tIdx].columns.push({
				name: '',
				data_type: 'TEXT',
				is_nullable: true,
				default_value: '',
				is_primary_key: false
			});
		},

		removeDiffColumn(tIdx, cIdx) {
			this.diffDesiredTables[tIdx].columns.splice(cIdx, 1);
		},

		async importCurrentSchemaIntoDiff() {
			if (!this.activeConnection) return;
			this.error = '';
			this.notice = '';
			try {
				const schema = this.diffSchemaName || this.selectedSchema || 'public';
				const tblData = await this.apiFetch(
					`/api/v1/connections/${encodeURIComponent(this.activeConnection.id)}/schemas/${encodeURIComponent(schema)}/tables`
				);
				const rawTables = Array.isArray(tblData) ? tblData : tblData?.tables || [];
				const imported = [];
				for (const t of rawTables) {
					const tName = typeof t === 'string' ? t : t.name || t.table_name;
					if (!tName) continue;
					const colData = await this.apiFetch(
						`/api/v1/connections/${encodeURIComponent(this.activeConnection.id)}/schemas/${encodeURIComponent(schema)}/tables/${encodeURIComponent(tName)}/columns`
					).catch(() => []);
					const rawCols = Array.isArray(colData) ? colData : colData?.columns || [];
					imported.push({
						name: tName,
						columns: rawCols.map((c) => ({
							name: c.name,
							data_type: c.dataType || c.data_type || c.type || 'TEXT',
							is_nullable: Boolean(c.nullable ?? c.is_nullable),
							default_value: c.default ?? c.column_default ?? c.defaultValue ?? '',
							is_primary_key: Boolean(c.primaryKey ?? c.is_primary_key)
						}))
					});
				}
				this.diffDesiredTables = imported;
				this.notice = `Loaded ${imported.length} table(s) from schema "${schema}". Modify them below and click Compare Schema.`;
			} catch (err) {
				this.error = err.message;
			}
		},

		async runSchemaDiff() {
			if (!this.activeConnection) return;
			this.runningSchemaDiff = true;
			this.error = '';
			try {
				const res = await this.apiFetch(`/api/v1/connections/${encodeURIComponent(this.activeConnection.id)}/migrations/schema-diff`, {
					method: 'POST',
					body: JSON.stringify({
						schema_name: this.diffSchemaName || 'public',
						desired_tables: this.diffDesiredTables
					})
				});
				this.schemaDiffResult = res;
			} catch (err) {
				this.error = err.message;
			} finally {
				this.runningSchemaDiff = false;
			}
		},

		loadDiffIntoMigrationDraft() {
			if (!this.schemaDiffResult || !this.schemaDiffResult.generated_sql) return;
			this.selectedMigration = null;
			this.migrationRuns = [];
			this.migrationForm = {
				title: `schema_diff_${this.diffSchemaName || 'public'}_${new Date().toISOString().slice(0, 10)}`,
				description: 'Auto-generated from Declarative Schema Diff',
				sql_content: this.schemaDiffResult.generated_sql
			};
			this.activeTab = 'migrations';
			this.notice = 'Generated DDL loaded into a new migration draft. Review and save when ready.';
		},

		// ==================== FORMATTERS ====================
		formatCellValue(val) {
			if (val === null || val === undefined) return 'NULL';
			if (typeof val === 'object') return JSON.stringify(val);
			return String(val);
		},

		formatDateTime(iso) {
			if (!iso) return '—';
			const d = new Date(iso);
			if (Number.isNaN(d.getTime())) return iso;
			return d.toLocaleString();
		}
	};
};
