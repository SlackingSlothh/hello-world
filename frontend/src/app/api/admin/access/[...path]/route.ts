const idPattern = /^[a-zA-Z0-9_-]+$/;

function allowed(method: string, path: string[]) {
  const [resource, groupId, action, userId] = path;
  if (path.length === 1) {
    return (resource === "users" || resource === "groups") &&
      (method === "GET" || method === "POST");
  }
  if (resource !== "groups" || !groupId || !idPattern.test(groupId)) {
    return false;
  }
  if (path.length === 3) {
    return (action === "members" && method === "GET") ||
      (action === "leader" && method === "PUT");
  }
  return path.length === 4 && action === "members" && idPattern.test(userId) &&
    (method === "PUT" || method === "DELETE");
}

async function forwardAccess(
  request: Request,
  context: RouteContext<"/api/admin/access/[...path]">,
) {
  const { path } = await context.params;
  const { method } = request;
  const responseHeaders = new Headers({ "Cache-Control": "private, no-store" });
  if (!allowed(method, path)) {
    return new Response(null, { status: 404, headers: responseHeaders });
  }

  const url = new URL(
    `/api/v1/access/${path.join("/")}`,
    process.env.INTERNAL_API_BASE_URL ?? "http://localhost:8080",
  );
  const headers = new Headers();
  for (const name of ["cookie", "content-type", "origin", "sec-fetch-site"]) {
    const value = request.headers.get(name);
    if (value) headers.set(name, value);
  }

  let upstream: Response;
  try {
    const options: RequestInit & { duplex: "half" } = {
      method,
      headers,
      body: request.body,
      duplex: "half",
      cache: "no-store",
      redirect: "manual",
    };
    upstream = await fetch(url, options);
  } catch (error) {
    if (!(error instanceof TypeError)) throw error;
    return Response.json(
      { error: { message: "Group service unavailable." } },
      { status: 503, headers: responseHeaders },
    );
  }

  const contentType = upstream.headers.get("content-type");
  if (contentType) responseHeaders.set("content-type", contentType);
  for (const cookie of upstream.headers.getSetCookie()) {
    responseHeaders.append("set-cookie", cookie);
  }
  return new Response(upstream.body, {
    status: upstream.status,
    headers: responseHeaders,
  });
}

export {
  forwardAccess as GET,
  forwardAccess as POST,
  forwardAccess as PUT,
  forwardAccess as DELETE,
};
