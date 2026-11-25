import { NextRequest, NextResponse } from "next/server";
import { backendAuthUrl } from "@/lib/server/backend-auth";

const adminHeader = "x-datadock-admin";
const usernameHeader = "x-datadock-username";

async function checkBackendSession(
  request: NextRequest,
  endpoint: "session" | "admin-session",
) {
  const headers = new Headers();
  const cookie = request.headers.get("cookie");
  if (cookie) headers.set("cookie", cookie);

  return fetch(backendAuthUrl(endpoint), {
    headers,
    cache: "no-store",
    redirect: "manual",
  });
}

function loginRedirect(request: NextRequest) {
  const destination = request.nextUrl.clone();
  const nextPath = `${destination.pathname}${destination.search}`;
  destination.pathname = "/login";
  destination.search = "";
  destination.searchParams.set("next", nextPath);
  return NextResponse.redirect(destination);
}

function authUnavailable() {
  return new NextResponse("Authentication service unavailable", {
    status: 503,
    headers: { "Cache-Control": "no-store" },
  });
}

function copySessionCookie(response: NextResponse, backendResponse: Response) {
  const cookie = backendResponse.headers.get("set-cookie");
  if (cookie) response.headers.set("set-cookie", cookie);
}

export async function proxy(request: NextRequest) {
  const isAdminRoute =
    request.nextUrl.pathname === "/admin" ||
    request.nextUrl.pathname.startsWith("/admin/");
  let sessionResponse: Response;
  let adminResponse: Response;
  let username: string;

  try {
    sessionResponse = await checkBackendSession(request, "session");
    if (sessionResponse.status === 401) return loginRedirect(request);
    if (sessionResponse.status !== 200) return authUnavailable();

    const profile: unknown = await sessionResponse.json();
    if (
      typeof profile !== "object" ||
      profile === null ||
      !("username" in profile) ||
      typeof profile.username !== "string" ||
      profile.username.length === 0
    ) {
      return authUnavailable();
    }
    username = profile.username;

    adminResponse = await checkBackendSession(request, "admin-session");
  } catch (error) {
    if (error instanceof TypeError) return authUnavailable();
    if (error instanceof SyntaxError) return authUnavailable();
    throw error;
  }

  if (adminResponse.status === 401) return loginRedirect(request);
  if (isAdminRoute && adminResponse.status === 403) {
    const notFoundUrl = request.nextUrl.clone();
    notFoundUrl.pathname = "/_not-found";
    notFoundUrl.search = "";
    const response = NextResponse.rewrite(notFoundUrl);
    response.headers.set("Cache-Control", "private, no-store");
    copySessionCookie(response, adminResponse);
    return response;
  }
  if (
    adminResponse.status !== 204 &&
    !(adminResponse.status === 403 && !isAdminRoute)
  ) {
    return authUnavailable();
  }

  const isAdmin = adminResponse.status === 204;
  const forwardedHeaders = new Headers(request.headers);
  forwardedHeaders.set(adminHeader, String(isAdmin));
  forwardedHeaders.set(usernameHeader, username);
  const response = NextResponse.next({
    request: { headers: forwardedHeaders },
  });
  response.headers.set("Cache-Control", "private, no-store");
  copySessionCookie(response, adminResponse);
  if (!response.headers.has("set-cookie")) {
    copySessionCookie(response, sessionResponse);
  }
  return response;
}

export const config = {
  matcher: ["/admin/:path*", "/db/:path*", "/migration/:path*"],
};
