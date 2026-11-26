import { NextRequest, NextResponse } from "next/server";
import { backendAuthUrl } from "@/lib/server/backend-auth";

const adminHeader = "x-datadock-admin";
const authStateHeader = "x-datadock-auth-state";
const usernameHeader = "x-datadock-username";

type AuthState = "anonymous" | "authenticated" | "unavailable";

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

function continueWithAuthState(
  request: NextRequest,
  state: AuthState,
  sessionResponse?: Response,
  adminResponse?: Response,
  username?: string,
): NextResponse {
  const isPublicRoot = request.nextUrl.pathname === "/";
  const forwardedHeaders = new Headers(request.headers);
  forwardedHeaders.delete(adminHeader);
  forwardedHeaders.delete(authStateHeader);
  forwardedHeaders.delete(usernameHeader);
  forwardedHeaders.set(authStateHeader, state);
  forwardedHeaders.set(adminHeader, String(adminResponse?.status === 204));
  if (username) forwardedHeaders.set(usernameHeader, username);

  const response = NextResponse.next({
    request: { headers: forwardedHeaders },
  });
  if (!isPublicRoot) {
    response.headers.set("Cache-Control", "private, no-store");
  }
  if (adminResponse) copySessionCookie(response, adminResponse);
  if (!response.headers.has("set-cookie") && sessionResponse) {
    copySessionCookie(response, sessionResponse);
  }
  return response;
}

export async function proxy(request: NextRequest) {
  const isPublicRoot = request.nextUrl.pathname === "/";
  const isAdminRoute =
    request.nextUrl.pathname === "/admin" ||
    request.nextUrl.pathname.startsWith("/admin/");
  if (isPublicRoot && !request.cookies.get("session_id")?.value) {
    return continueWithAuthState(request, "anonymous");
  }

  let sessionResponse: Response;
  let adminResponse: Response;
  let username: string;

  try {
    sessionResponse = await checkBackendSession(request, "session");
    if (sessionResponse.status === 401) {
      if (isPublicRoot) {
        return continueWithAuthState(request, "anonymous", sessionResponse);
      }
      return loginRedirect(request);
    }
    if (sessionResponse.status !== 200) {
      if (isPublicRoot) {
        return continueWithAuthState(request, "unavailable", sessionResponse);
      }
      return authUnavailable();
    }

    const profile: unknown = await sessionResponse.json();
    if (
      typeof profile !== "object" ||
      profile === null ||
      !("username" in profile) ||
      typeof profile.username !== "string" ||
      profile.username.length === 0
    ) {
      if (isPublicRoot) {
        return continueWithAuthState(request, "unavailable", sessionResponse);
      }
      return authUnavailable();
    }
    username = profile.username;

    adminResponse = await checkBackendSession(request, "admin-session");
  } catch (error) {
    if (isPublicRoot) {
      return continueWithAuthState(request, "unavailable");
    }
    if (error instanceof TypeError) return authUnavailable();
    if (error instanceof SyntaxError) return authUnavailable();
    throw error;
  }

  if (adminResponse.status === 401) {
    if (isPublicRoot) {
      return continueWithAuthState(
        request,
        "anonymous",
        sessionResponse,
        adminResponse,
      );
    }
    return loginRedirect(request);
  }
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
    if (isPublicRoot) {
      return continueWithAuthState(
        request,
        "unavailable",
        sessionResponse,
        adminResponse,
      );
    }
    return authUnavailable();
  }

  return continueWithAuthState(
    request,
    "authenticated",
    sessionResponse,
    adminResponse,
    username,
  );
}

export const config = {
  matcher: ["/", "/admin/:path*", "/db/:path*", "/migration/:path*"],
};
