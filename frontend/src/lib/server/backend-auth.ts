import "server-only";

export type BackendAuthEndpoint = "session" | "admin-session" | "login" | "logout";

export function backendAuthUrl(endpoint: BackendAuthEndpoint): URL {
  const baseUrl =
    process.env.INTERNAL_API_BASE_URL ?? "http://localhost:8080";
  return new URL(`/auth/${endpoint}`, baseUrl);
}
