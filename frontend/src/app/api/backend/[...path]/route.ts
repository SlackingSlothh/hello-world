import { backendBaseUrl } from "@/lib/server/backend-auth";

const maxRequestBodyBytes = 1024 * 1024;
const allowedMethods = new Set(["GET", "POST", "PUT", "PATCH", "DELETE"]);

type RouteContext = {
  params: Promise<{ path: string[] }>;
};

function sessionCookieHeader(cookieHeader: string | null): string | undefined {
  const sessionCookies = (cookieHeader ?? "")
    .split(";")
    .map((cookie) => cookie.trim())
    .filter((cookie) => cookie.slice(0, cookie.indexOf("=")).trim() === "session_id");
  return sessionCookies.length > 0 ? sessionCookies.join("; ") : undefined;
}

async function forward(request: Request, context: RouteContext) {
  const { path } = await context.params;
  if (
    !allowedMethods.has(request.method) ||
    path[0] !== "v1" ||
    path.some(
      (segment) =>
        segment === "." ||
        segment === ".." ||
        !/^[a-zA-Z0-9._~-]+$/.test(segment),
    )
  ) {
    return new Response("Not found", { status: 404 });
  }

  const contentLength = Number(request.headers.get("content-length"));
  if (
    Number.isFinite(contentLength) &&
    contentLength > maxRequestBodyBytes
  ) {
    return new Response("Request body too large", { status: 413 });
  }

  const body =
    request.method === "GET" || request.method === "DELETE"
      ? undefined
      : await request.arrayBuffer();
  if (body && body.byteLength > maxRequestBodyBytes) {
    return new Response("Request body too large", { status: 413 });
  }

  const url = backendBaseUrl();
  url.pathname = `/api/${path.map(encodeURIComponent).join("/")}`;
  url.search = new URL(request.url).search;

  const headers = new Headers();
  for (const name of ["content-type", "origin"]) {
    const value = request.headers.get(name);
    if (value) headers.set(name, value);
  }
  const sessionCookie = sessionCookieHeader(request.headers.get("cookie"));
  if (sessionCookie) headers.set("cookie", sessionCookie);

  let backendResponse: Response;
  try {
    backendResponse = await fetch(url, {
      method: request.method,
      headers,
      body,
      cache: "no-store",
      redirect: "manual",
    });
  } catch (error) {
    if (error instanceof TypeError) {
      return Response.json(
        { error: { code: "api_unavailable", message: "Backend API unavailable." } },
        { status: 503, headers: { "Cache-Control": "no-store" } },
      );
    }
    throw error;
  }

  const responseHeaders = new Headers({ "Cache-Control": "no-store" });
  for (const name of ["content-type", "set-cookie"]) {
    const value = backendResponse.headers.get(name);
    if (value) responseHeaders.set(name, value);
  }

  return new Response(backendResponse.body, {
    status: backendResponse.status,
    headers: responseHeaders,
  });
}

export const GET = forward;
export const POST = forward;
export const PUT = forward;
export const PATCH = forward;
export const DELETE = forward;
