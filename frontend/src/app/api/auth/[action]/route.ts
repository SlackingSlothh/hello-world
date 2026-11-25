import { backendAuthUrl } from "@/lib/server/backend-auth";

const postActions = new Set(["login", "logout"]);
const maxAuthBodyBytes = 8 * 1024;

type RouteContext = {
  params: Promise<{ action: string }>;
};

function isPostAction(action: string): action is "login" | "logout" {
  return postActions.has(action);
}

export async function POST(request: Request, context: RouteContext) {
  const { action } = await context.params;
  if (!isPostAction(action)) {
    return new Response("Not found", { status: 404 });
  }
  if (new URL(request.url).search) {
    return new Response("Query parameters are not accepted", { status: 400 });
  }

  const contentLength = Number(request.headers.get("content-length"));
  if (Number.isFinite(contentLength) && contentLength > maxAuthBodyBytes) {
    return new Response("Request body too large", { status: 413 });
  }

  const body = await request.text();
  if (new TextEncoder().encode(body).byteLength > maxAuthBodyBytes) {
    return new Response("Request body too large", { status: 413 });
  }

  const headers = new Headers();
  for (const name of ["content-type", "cookie", "origin"]) {
    const value = request.headers.get(name);
    if (value) headers.set(name, value);
  }

  let backendResponse: Response;
  try {
    backendResponse = await fetch(backendAuthUrl(action), {
      method: "POST",
      headers,
      body,
      cache: "no-store",
      redirect: "manual",
    });
  } catch (error) {
    if (error instanceof TypeError) {
      return new Response("Authentication service unavailable", {
        status: 503,
        headers: { "Cache-Control": "no-store" },
      });
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
