import { defineMiddleware } from 'astro:middleware';

const apiBase = (process.env.INTERNAL_API_BASE_URL || import.meta.env.PUBLIC_API_BASE_URL || '').replace(/\/+$/, '');

function redirectToLogin(request: Request) {
	const current = new URL(request.url);
	const login = new URL('/login', current);
	login.searchParams.set('returnTo', `${current.pathname}${current.search}`);
	return Response.redirect(login, 302);
}

function redirectWithSessionCookies(destination: URL, sessionResponse: Response) {
	const headers = new Headers({ location: destination.toString() });
	for (const cookie of sessionResponse.headers.getSetCookie()) headers.append('Set-Cookie', cookie);
	return new Response(null, { status: 302, headers });
}

export const onRequest = defineMiddleware(async (context, next) => {
	const pathname = context.url.pathname;
	const adminRoute = pathname === '/admin' || pathname === '/admin/' || pathname.startsWith('/admin/');
	const userRoute = pathname === '/db' || pathname === '/db/' || pathname.startsWith('/db/');
	if (!adminRoute && !userRoute) return next();

	const cookie = context.request.headers.get('cookie');
	if (!cookie) return redirectToLogin(context.request);
	if (!apiBase) return new Response('Authentication service unavailable', { status: 503 });

	let response: Response;
	try {
		response = await fetch(`${apiBase}/auth/${adminRoute ? 'admin-session' : 'session'}`, {
			headers: { cookie },
			cache: 'no-store',
		});
	} catch {
		return new Response('Authentication service unavailable', { status: 503 });
	}

	if (response.status === 401) return redirectToLogin(context.request);
	if (adminRoute && response.status === 403) return context.rewrite('/not-found');
	if (!response.ok) return new Response('Authentication service unavailable', { status: 503 });
	if (pathname === '/admin' || pathname === '/admin/') {
		return redirectWithSessionCookies(new URL('/admin/db', context.url), response);
	}
	const page = await next();
	const headers = new Headers(page.headers);
	for (const cookie of response.headers.getSetCookie()) headers.append('Set-Cookie', cookie);
	headers.set('Cache-Control', 'private, no-store');
	return new Response(page.body, { status: page.status, statusText: page.statusText, headers });
});
