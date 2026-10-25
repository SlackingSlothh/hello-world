import Alpine from 'alpinejs';
import databaseWorkspace from './databaseWorkspace.js';
import editConnectionForm from './editConnectionForm.js';

Alpine.data('databaseWorkspace', databaseWorkspace);
Alpine.data('editConnectionForm', editConnectionForm);

const apiBase = (document.documentElement.dataset.apiBase || '').replace(/\/+$/, '');
const apiURL = (path) => `${apiBase}${path}`;

document.addEventListener('click', async (event) => {
	const button = event.target.closest('[data-logout]');
	if (!button) return;
	button.disabled = true;
	try {
		const response = await fetch(apiURL('/auth/logout'), {
			method: 'POST',
			headers: { 'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8' },
			credentials: 'include',
			body: new URLSearchParams(),
		});
		if (!response.ok) throw new Error('Sign out failed. Please try again.');
		window.location.assign('/login');
	} catch (error) {
		window.alert(error.message || 'Sign out failed. Please try again.');
		button.disabled = false;
	}
});

Alpine.data('connectionDashboard', (admin = false) => ({
	admin,
	connections: [],
	loading: true,
	error: '',
	filter: '',
	busy: '',
	init() {
		window.addEventListener('datadock:workspace-error', event => { this.error = event.detail; });
		this.loadConnections();
	},
	async loadConnections() {
		this.loading = true;
		this.error = '';
		try {
			const response = await fetch(apiURL('/api/v1/connections'), { credentials: 'include', cache: 'no-store' });
			if (response.status === 401) throw new Error('Your session is not valid. Sign in to continue.');
			if (!response.ok) throw new Error('Could not load connections from the database API.');
			const items = await response.json();
			if (!Array.isArray(items)) throw new Error('The server returned an invalid connection list.');
			await this.loadStatuses(items);
		} catch (error) {
			this.error = error.message || 'Could not load connections from the database API.';
			this.loading = false;
		}
	},
	async loadStatuses(items) {
		this.error = '';
		this.loading = false;
		this.connections = items.map((item) => ({ ...item, reachable: item.connected ? null : false, checking: item.connected }));
		await Promise.all(this.connections.map(async (connection) => {
			if (!connection.connected) return;
			try {
				const response = await fetch(apiURL(`/api/v1/connections/${encodeURIComponent(connection.id)}/test`), { method: 'POST', credentials: 'include' });
				connection.reachable = response.ok;
			} catch { connection.reachable = false; }
			connection.checking = false;
		}));
	},
	get visibleConnections() {
		const query = this.filter.trim().toLowerCase();
		return query ? this.connections.filter((connection) => `${connection.name} ${connection.driver}`.toLowerCase().includes(query)) : this.connections;
	},
	async action(connection, action) {
		this.busy = `${connection.id}:${action}`;
		try {
			const response = await fetch(apiURL(`/api/v1/connections/${encodeURIComponent(connection.id)}/${action}`), {
				method: 'POST', credentials: 'include',
			});
			if (!response.ok) throw new Error(action === 'connect' ? 'Could not connect to this database.' : 'Could not disconnect this database.');
			await this.loadConnections();
		} catch (error) { this.error = error.message || 'The connection action failed.'; }
		finally { this.busy = ''; }
	},
	async remove(connection) {
		if (!window.confirm(`Delete “${connection.name}”? This removes its saved connection settings.`)) return;
		this.busy = `${connection.id}:delete`;
		try {
			const response = await fetch(apiURL(`/api/v1/connections/${encodeURIComponent(connection.id)}`), { method: 'DELETE', credentials: 'include' });
			if (!response.ok) throw new Error('Could not delete this connection.');
			await this.loadConnections();
		} catch (error) { this.error = error.message || 'The delete action failed.'; }
		finally { this.busy = ''; }
	},
}));

Alpine.data('newConnectionForm', () => ({
	name: '', driver: 'postgres', dsn: '', maxOpen: 10, maxIdle: 2, submitting: false, error: '',
	async submit() {
		this.submitting = true;
		this.error = '';
		try {
			const response = await fetch(apiURL('/api/v1/connections'), {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				credentials: 'include',
				body: JSON.stringify({ name: this.name.trim(), driver: this.driver, dsn: this.dsn.trim(), pool: { maxOpen: Number(this.maxOpen), maxIdle: Number(this.maxIdle) } }),
			});
			if (!response.ok) {
				const body = await response.json().catch(() => ({}));
				throw new Error(body.error?.message || 'Could not create the connection. Check the values and database availability.');
			}
			window.location.assign('/admin/db');
		} catch (error) { this.error = error.message || 'Could not create the connection.'; }
		finally { this.submitting = false; }
	},
}));

Alpine.data('groupAccessManager', () => ({
	groups: [],
	groupName: '',
	memberGroupID: '',
	memberUserID: '',
	groupUsers: [],
	membersLoading: false,
	memberRequest: 0,
	connections: [],
	groupID: '',
	databaseID: '',
	access: 'read',
	loading: true,
	busy: false,
	error: '',
	notice: '',
	init() { this.load(); },
	get members() { return this.groupUsers.filter(user => user.member); },
	get availableUsers() { return this.groupUsers.filter(user => !user.member && user.active); },
	async loadMembers() {
		const request = ++this.memberRequest;
		const groupID = this.memberGroupID;
		this.groupUsers = [];
		this.memberUserID = '';
		this.error = '';
		this.membersLoading = true;
		try {
			if (!groupID) return;
			const response = await fetch(apiURL(`/api/v1/access/groups/${encodeURIComponent(groupID)}/members`), { credentials: 'include', cache: 'no-store' });
			if (!response.ok) throw new Error('Could not load group members. Check your session and permissions.');
			const users = await response.json();
			if (request !== this.memberRequest) return;
			this.groupUsers = users;
			this.memberUserID = this.availableUsers[0]?.id || '';
		} catch (error) {
			if (request === this.memberRequest) this.error = error.message || 'Could not load group members.';
		} finally {
			if (request === this.memberRequest) this.membersLoading = false;
		}
	},
	async setMember(userID, member) {
		if (this.busy || this.membersLoading || !this.memberGroupID || !userID) return;
		const group = this.groups.find(group => group.id === this.memberGroupID);
		if (!member && !window.confirm('Remove this member? Their active sessions will end and they must sign in again.')) return;
		if (member && group?.isAdmin && !window.confirm('This group grants Platform Admin permissions. Add this user as an administrator?')) return;
		this.busy = true;
		this.error = '';
		this.notice = '';
		try {
			const response = await fetch(apiURL(`/api/v1/access/groups/${encodeURIComponent(this.memberGroupID)}/members/${encodeURIComponent(userID)}`), {
				method: member ? 'PUT' : 'DELETE', credentials: 'include',
			});
			if (!response.ok) {
				const result = await response.json().catch(() => ({}));
				throw new Error(result.error?.message || 'Could not update group membership. Check your session and permissions.');
			}
			this.notice = member ? 'Member added.' : 'Member removed. Their active sessions have ended.';
			await this.loadMembers();
		} catch (error) { this.error = error.message || 'Could not update group membership.'; }
		finally { this.busy = false; }
	},

	async createGroup() {
		if (this.busy || this.loading) return;
		this.busy = true;
		this.error = '';
		this.notice = '';
		try {
			const response = await fetch(apiURL('/api/v1/access/groups'), {
				method: 'POST', credentials: 'include',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ name: this.groupName }),
			});
			if (response.status === 401) throw new Error('Your session has expired. Sign in again.');
			if (response.status === 403) throw new Error('Only platform admins can add groups.');
			const result = await response.json().catch(() => ({}));
			if (!response.ok) throw new Error(result.error?.message || 'Could not add group.');
			this.groups.push(result);
			this.groups.sort((a, b) => a.name.localeCompare(b.name));
			this.groupID = result.id;
			this.memberGroupID = result.id;
			await this.loadMembers();
			this.groupName = '';
			this.notice = `Group “${result.name}” added without members or database access.`;
		} catch (error) { this.error = error.message || 'Could not add group.'; }
		finally { this.busy = false; }
	},
	async load() {
		this.loading = true;
		this.error = '';
		try {
			const response = await fetch(apiURL('/api/v1/access/groups'), { credentials: 'include', cache: 'no-store' });
			if (!response.ok) throw new Error('Could not load group access settings.');
			const result = await response.json();
			this.groups = result.groups || [];
			this.connections = result.connections || [];
			this.groupID ||= this.groups[0]?.id || '';
			this.databaseID ||= this.connections[0]?.id || '';
			this.memberGroupID ||= this.groups[0]?.id || '';
			await this.loadMembers();
		} catch (error) {
			this.error = error.message || 'Could not load group access settings.';
		} finally { this.loading = false; }
	},
	connectionName(id) {
		const connection = this.connections.find((item) => item.id === id);
		return connection ? `${connection.name} (${connection.driver})` : id;
	},
	async save(groupID, databaseID, access) {
		const response = await fetch(apiURL(`/api/v1/access/groups/${encodeURIComponent(groupID)}/connections/${encodeURIComponent(databaseID)}`), {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			credentials: 'include',
			body: JSON.stringify({ access }),
		});
		if (!response.ok) {
			const body = await response.json().catch(() => ({}));
			throw new Error(body.error?.message || 'Could not save group access.');
		}
	},
	async grant() {
		if (!this.groupID || !this.databaseID) return;
		this.busy = true;
		this.notice = '';
		this.error = '';
		try {
			await this.save(this.groupID, this.databaseID, this.access);
			await this.load();
			this.notice = 'Group access saved.';
		} catch (error) { this.error = error.message || 'Could not save group access.'; }
		finally { this.busy = false; }
	},
	async update(group, grant, access) {
		this.busy = true;
		this.notice = '';
		this.error = '';
		try {
			await this.save(group.id, grant.databaseId, access);
			await this.load();
			this.notice = 'Group access updated.';
		} catch (error) {
			this.error = error.message || 'Could not update group access.';
			await this.load();
		} finally { this.busy = false; }
	},
	async revoke(group, grant) {
		if (!window.confirm(`Revoke ${group.name}'s access to ${this.connectionName(grant.databaseId)}?`)) return;
		this.busy = true;
		this.notice = '';
		this.error = '';
		try {
			const response = await fetch(apiURL(`/api/v1/access/groups/${encodeURIComponent(group.id)}/connections/${encodeURIComponent(grant.databaseId)}`), {
				method: 'DELETE', credentials: 'include',
			});
			if (!response.ok) throw new Error('Could not revoke group access.');
			await this.load();
			this.notice = 'Group access revoked.';
		} catch (error) { this.error = error.message || 'Could not revoke group access.'; }
		finally { this.busy = false; }
	},
}));

Alpine.data('newUserForm', () => ({
	username: '', password: '', submitting: false, error: '', notice: '',
	async submit() {
		if (this.submitting) return;
		this.submitting = true;
		this.error = '';
		this.notice = '';
		try {
			const response = await fetch(apiURL('/api/v1/access/users'), {
				method: 'POST', credentials: 'include',
				headers: { 'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8' },
				body: new URLSearchParams({ username: this.username, password: this.password }),
			});
			if (response.status === 401) throw new Error('Your session has expired. Sign in again.');
			if (response.status === 403) throw new Error('Only platform admins can add users.');
			const result = await response.json().catch(() => ({}));
			if (!response.ok) throw new Error(result.error?.message || 'Could not add user.');
			this.notice = `User “${result.username}” added. Database access requires group membership.`;
			this.username = '';
		} catch (error) { this.error = error.message || 'Could not add user.'; }
		finally { this.password = ''; this.submitting = false; }
	},
}));

Alpine.data('loginForm', () => ({
	username: '',
	password: '',
	submitting: false,
	error: '',
	async submit() {
		this.submitting = true;
		this.error = '';
		try {
			const form = new URLSearchParams({ username: this.username.trim(), password: this.password });
			const response = await fetch(apiURL('/auth/login'), {
				method: 'POST',
				headers: { 'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8' },
				credentials: 'include',
				body: form,
			});
			if (!response.ok) {
				if (response.status === 401) throw new Error('The username or password is incorrect.');
				if (response.status === 503) throw new Error('Sign in is temporarily unavailable. Please try again.');
				throw new Error('Could not sign in. Check your details and try again.');
			}
			const requested = new URLSearchParams(window.location.search).get('returnTo') || '/db';
			const destination = new URL(requested, window.location.origin);
			const safePath = destination.origin === window.location.origin && destination.pathname !== '/login'
				? `${destination.pathname}${destination.search}${destination.hash}`
				: '/db';
			window.location.assign(safePath);
		} catch (error) {
			this.error = error.message || 'Could not sign in. Please try again.';
		} finally {
			this.submitting = false;
		}
	},
}));

window.Alpine = Alpine;
Alpine.start();
