import { z } from "zod";
import type {
  AccessGrant,
  ConnectionFormValues,
  ConnectionPoolValues,
  ConnectionSummary,
} from "@/components/admin/admin-panels";

const poolSchema = z.object({
  maxOpen: z.number(),
  maxIdle: z.number(),
  maxLifetimeSeconds: z.number().optional(),
  maxIdleTimeSeconds: z.number().optional(),
});

const connectionSchema = z.object({
  id: z.string(),
  name: z.string(),
  driver: z.enum(["postgres", "mysql", "mariadb"]),
  connected: z.boolean(),
  pool: poolSchema,
  poolStats: z.object({
    OpenConnections: z.number(),
    Idle: z.number(),
  }),
});

const groupSchema = z.object({
  id: z.string(),
  name: z.string(),
  isAdmin: z.boolean(),
  grants: z.array(
    z.object({
      groupId: z.string(),
      databaseId: z.string(),
      access: z.enum(["read", "write"]),
    }),
  ),
});

const groupsResponseSchema = z.object({
  groups: z.array(groupSchema),
  connections: z.array(
    z.object({ id: z.string(), name: z.string(), driver: z.string() }),
  ),
});

type BackendConnection = z.infer<typeof connectionSchema>;
type BackendGroup = z.infer<typeof groupSchema>;

export type AdminGroupData = {
  groups: BackendGroup[];
  connections: Array<{ id: string; name: string; driver: string }>;
};

export class AdminApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
    this.name = "AdminApiError";
  }
}

function connectionSummary(connection: BackendConnection): ConnectionSummary {
  return {
    id: connection.id,
    name: connection.name,
    engine: connection.driver,
    status: connection.connected ? "connected" : "disconnected",
    activeConnections: connection.poolStats.OpenConnections - connection.poolStats.Idle,
    idleConnections: connection.poolStats.Idle,
    maxOpenConnections: connection.pool.maxOpen,
    maxIdleConnections: connection.pool.maxIdle,
    maxLifetimeSeconds: connection.pool.maxLifetimeSeconds ?? 0,
    maxIdleTimeSeconds: connection.pool.maxIdleTimeSeconds ?? 0,
  };
}

async function requestResponse(
  path: string,
  init: RequestInit = {},
): Promise<Response> {
  let response: Response;
  const headers = new Headers(init.headers);
  if (init.body) headers.set("Content-Type", "application/json");
  try {
    response = await fetch(`/api/backend/v1${path}`, {
      ...init,
      cache: "no-store",
      headers,
    });
  } catch (error) {
    if (error instanceof TypeError) {
      throw new AdminApiError("Could not reach the backend API.", 0);
    }
    throw error;
  }

  if (response.ok) return response;

  let message = `Backend request failed (HTTP ${response.status}).`;
  try {
    const body: unknown = await response.json();
    if (
      typeof body === "object" &&
      body !== null &&
      "error" in body &&
      typeof body.error === "object" &&
      body.error !== null &&
      "message" in body.error &&
      typeof body.error.message === "string"
    ) {
      message = body.error.message;
    }
  } catch (error) {
    if (!(error instanceof SyntaxError)) throw error;
  }
  throw new AdminApiError(message, response.status);
}

async function requestJson<T>(
  path: string,
  schema: z.ZodType<T>,
  init: RequestInit = {},
): Promise<T> {
  const response = await requestResponse(path, init);
  let body: unknown;
  try {
    body = await response.json();
  } catch (error) {
    if (error instanceof SyntaxError) {
      throw new Error("Backend returned invalid JSON.");
    }
    throw error;
  }
  return schema.parse(body);
}

async function requestVoid(path: string, init: RequestInit): Promise<void> {
  await requestResponse(path, init);
}

function jsonBody(value: unknown): string {
  return JSON.stringify(value);
}

function encodePath(value: string): string {
  return encodeURIComponent(value);
}

function escapeDsnPart(value: string): string {
  return encodeURIComponent(value).replace(/[!'()*]/g, (character) =>
    `%${character.charCodeAt(0).toString(16).toUpperCase()}`,
  );
}

export function buildConnectionDsn(values: ConnectionFormValues): string {
  if (values.engine === "postgres") {
    const url = new URL("postgresql://localhost");
    url.username = values.username;
    url.password = values.password;
    url.hostname =
      values.host.includes(":") && !values.host.startsWith("[")
        ? `[${values.host}]`
        : values.host;
    url.port = String(values.port);
    url.pathname = `/${encodeURIComponent(values.database)}`;
    url.searchParams.set("sslmode", values.sslMode);
    url.searchParams.set(
      "connect_timeout",
      String(values.connectionTimeoutSeconds),
    );
    return url.toString();
  }

  const host = values.host.includes(":") && !values.host.startsWith("[")
    ? `[${values.host}]`
    : values.host;
  const tls =
    values.sslMode === "disable"
      ? "false"
      : values.sslMode === "prefer"
        ? "preferred"
        : "true";
  const query = new URLSearchParams({
    timeout: `${values.connectionTimeoutSeconds}s`,
    tls,
  });
  return `${escapeDsnPart(values.username)}:${escapeDsnPart(values.password)}@tcp(${host}:${values.port})/${escapeDsnPart(values.database)}?${query}`;
}

function poolFromForm(values: ConnectionPoolValues) {
  return {
    maxOpen: values.maxOpenConnections,
    maxIdle: values.maxIdleConnections,
    maxLifetimeSeconds: values.maxLifetimeSeconds,
    maxIdleTimeSeconds: values.maxIdleTimeSeconds,
  };
}

export async function listAdminConnections(): Promise<ConnectionSummary[]> {
  const connections = await requestJson(
    "/admin/connections",
    z.array(connectionSchema),
  );
  return connections.map(connectionSummary);
}

export async function createConnection(
  values: ConnectionFormValues,
): Promise<ConnectionSummary> {
  const connection = await requestJson(
    "/connections",
    connectionSchema,
    {
      method: "POST",
      body: jsonBody({
        name: values.name,
        driver: values.engine,
        dsn: buildConnectionDsn(values),
        pool: poolFromForm(values),
      }),
    },
  );
  return connectionSummary(connection);
}

export async function updateConnection(
  id: string,
  values: ConnectionPoolValues,
): Promise<ConnectionSummary> {
  const connection = await requestJson(
    `/connections/${encodePath(id)}`,
    connectionSchema,
    {
      method: "PATCH",
      body: jsonBody({ pool: poolFromForm(values) }),
    },
  );
  return connectionSummary(connection);
}

export async function setConnectionConnected(
  id: string,
  connected: boolean,
): Promise<ConnectionSummary | void> {
  const suffix = connected ? "connect" : "disconnect";
  const init = { method: "POST" };
  if (!connected) {
    await requestVoid(`/connections/${encodePath(id)}/${suffix}`, init);
    return;
  }
  const connection = await requestJson(
    `/connections/${encodePath(id)}/${suffix}`,
    connectionSchema,
    init,
  );
  return connectionSummary(connection);
}

export async function deleteConnection(id: string): Promise<void> {
  await requestVoid(`/connections/${encodePath(id)}`, { method: "DELETE" });
}

export async function listGroupAccess(): Promise<AdminGroupData> {
  return requestJson("/access/groups", groupsResponseSchema);
}

export async function setGroupConnectionAccess(
  groupId: string,
  connectionId: string,
  access: "read" | "write" | null,
): Promise<void> {
  const path = `/access/groups/${encodePath(groupId)}/connections/${encodePath(connectionId)}`;
  if (access === null) {
    await requestVoid(path, { method: "DELETE" });
    return;
  }
  await requestVoid(path, {
    method: "PUT",
    body: jsonBody({ access }),
  });
}

export function accessGrantLevel(access: "read" | "write"): AccessGrant["level"] {
  return access === "read" ? "view" : "view_edit";
}
