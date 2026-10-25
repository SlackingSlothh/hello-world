export default function editConnectionForm() {
	return {
		id: '', name: '', driver: 'postgres', originalDriver: '', dsn: '', pool: {},
		maxOpen: 0, maxIdle: 0, loading: true, loaded: false, submitting: false, error: '', notice: '',
		get apiBase() { return (document.documentElement.dataset.apiBase || '').replace(/\/+$/, ''); },
		async init() {
			this.id = new URLSearchParams(window.location.search).get('id') || '';
			try {
				if (!this.id) throw new Error('Select a database connection to edit.');
				const response = await fetch(`${this.apiBase}/api/v1/connections/${encodeURIComponent(this.id)}/settings`, { credentials: 'include', cache: 'no-store' });
				if (!response.ok) throw new Error('Could not load connection settings. Check your session and admin permissions.');
				const connection = await response.json();
				this.name = connection.name;
				this.driver = connection.driver;
				this.originalDriver = connection.driver;
				this.pool = connection.pool || {};
				this.maxOpen = this.pool.maxOpen ?? 0;
				this.maxIdle = this.pool.maxIdle ?? 0;
				this.loaded = true;
			} catch (error) { this.error = error.message || 'Could not load connection settings.'; }
			finally { this.loading = false; }
		},
		async submit() {
			if (!this.loaded || this.submitting) return;
			this.error = '';
			this.notice = '';
			if (this.driver !== this.originalDriver && !this.dsn.trim()) {
				this.error = 'Enter a new DSN when changing the database engine.';
				return;
			}
			this.submitting = true;
			try {
				const response = await fetch(`${this.apiBase}/api/v1/connections/${encodeURIComponent(this.id)}`, {
					method: 'PATCH', credentials: 'include',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ name: this.name.trim(), driver: this.driver, dsn: this.dsn.trim(), pool: { ...this.pool, maxOpen: Number(this.maxOpen), maxIdle: Number(this.maxIdle) } }),
				});
				const result = await response.json().catch(() => ({}));
				if (!response.ok) throw new Error(result.error?.message || 'Could not save settings. Check your session and admin permissions.');
				this.name = result.name;
				this.driver = result.driver;
				this.originalDriver = result.driver;
				this.pool = result.pool;
				this.notice = 'Connection settings saved.';
			} catch (error) { this.error = error.message || 'Could not save connection settings.'; }
			finally { this.dsn = ''; this.submitting = false; }
		},
	};
}
