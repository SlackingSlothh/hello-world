import { z } from "zod";
import type {
  DatabaseSummary,
  SchemaNode,
} from "@/components/database/database-panels";

const connectionSchema = z.object({
  id: z.string(),
  name: z.string(),
  driver: z.enum(["postgres", "mysql", "mariadb"]),
  connected: z.boolean(),
});

const schemaSchema = z.object({ name: z.string() });
const tableSchema = z.object({
  schema: z.string(),
  name: z.string(),
  type: z.string(),
});
const columnSchema = z.object({
  name: z.string(),
  position: z.number(),
  type: z.string(),
  nullable: z.boolean(),
  default: z.unknown().optional(),
  ordinal: z.number(),
});
const constraintSchema = z.object({
  name: z.string(),
  type: z.string(),
  columns: z.array(z.string()),
  definition: z.string().optional(),
});
const indexSchema = z.object({
  name: z.string(),
  unique: z.boolean(),
  method: z.string().optional(),
  columns: z.array(z.string()),
});
const rowsPageSchema = z.object({
  columns: z.array(z.string()),
  rows: z.array(z.array(z.unknown())),
  limit: z.number(),
  offset: z.number(),
});
const queryResultSchema = z.object({
  columns: z.array(z.string()),
  rows: z.array(z.array(z.unknown())),
});

export type DatabaseColumn = z.infer<typeof columnSchema>;
export type DatabaseConstraint = z.infer<typeof constraintSchema>;
export type DatabaseIndex = z.infer<typeof indexSchema>;
export type DatabaseRowsPage = z.infer<typeof rowsPageSchema>;
export type DatabaseQueryResult = z.infer<typeof queryResultSchema>;

export class DatabaseApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
    this.name = "DatabaseApiError";
  }
}

async function requestJson<T>(
  path: string,
  schema: z.ZodType<T>,
  init: RequestInit = {},
): Promise<T> {
  let response: Response;
  try {
    response = await fetch(`/api/backend/v1${path}`, {
      cache: "no-store",
      ...init,
    });
  } catch (error) {
    if (error instanceof TypeError) {
      throw new DatabaseApiError("Could not reach the backend API.", 0);
    }
    throw error;
  }

  if (!response.ok) {
    let message =
      response.status === 401
        ? "Your session expired. Sign in again."
        : response.status === 403 || response.status === 404
          ? "This database is unavailable or you do not have access."
          : `Database request failed (HTTP ${response.status}).`;
    try {
      const body: unknown = await response.json();
      if (
        typeof body === "object" &&
        body !== null &&
        "error" in body &&
        typeof body.error === "object" &&
        body.error !== null &&
        "message" in body.error &&
        typeof body.error.message === "string" &&
        response.status < 500
      ) {
        message = body.error.message;
      }
    } catch (error) {
      if (!(error instanceof SyntaxError)) throw error;
    }
    throw new DatabaseApiError(message, response.status);
  }

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

function connectionSummary(
  connection: z.infer<typeof connectionSchema>,
): DatabaseSummary {
  return {
    id: connection.id,
    name: connection.name,
    engine: connection.driver,
    status: connection.connected ? "connected" : "disconnected",
  };
}

function encodePath(value: string) {
  return encodeURIComponent(value);
}

export async function listDatabaseConnections(
  signal?: AbortSignal,
): Promise<DatabaseSummary[]> {
  const connections = await requestJson(
    "/connections",
    z.array(connectionSchema),
    { signal },
  );
  return connections.map(connectionSummary);
}

export async function getDatabaseConnection(
  id: string,
  signal?: AbortSignal,
): Promise<DatabaseSummary> {
  const connection = await requestJson(
    `/connections/${encodePath(id)}`,
    connectionSchema,
    { signal },
  );
  if (connection.id !== id) {
    throw new Error("Backend returned a different database connection.");
  }
  return connectionSummary(connection);
}

export async function loadDatabaseSchemas(
  id: string,
  signal?: AbortSignal,
): Promise<SchemaNode[]> {
  const schemas = await requestJson(
    `/connections/${encodePath(id)}/schemas`,
    z.array(schemaSchema).nullable().transform((items) => items ?? []),
    { signal },
  );
  return Promise.all(
    schemas.map(async ({ name }) => {
      const tables = await requestJson(
        `/connections/${encodePath(id)}/schemas/${encodePath(name)}/tables`,
        z.array(tableSchema).nullable().transform((items) => items ?? []),
        { signal },
      );
      return { name, tables: tables.map((table) => table.name) };
    }),
  );
}

function tablePath(id: string, schema: string, table: string) {
  return `/connections/${encodePath(id)}/schemas/${encodePath(schema)}/tables/${encodePath(table)}`;
}

export function loadDatabaseColumns(
  id: string,
  schema: string,
  table: string,
  signal?: AbortSignal,
) {
  return requestJson(
    `${tablePath(id, schema, table)}/columns`,
    z.array(columnSchema).nullable().transform((items) => items ?? []),
    { signal },
  );
}

export function loadDatabaseConstraints(
  id: string,
  schema: string,
  table: string,
  signal?: AbortSignal,
) {
  return requestJson(
    `${tablePath(id, schema, table)}/constraints`,
    z.array(constraintSchema).nullable().transform((items) => items ?? []),
    { signal },
  );
}

export function loadDatabaseIndexes(
  id: string,
  schema: string,
  table: string,
  signal?: AbortSignal,
) {
  return requestJson(
    `${tablePath(id, schema, table)}/indexes`,
    z.array(indexSchema).nullable().transform((items) => items ?? []),
    { signal },
  );
}

export function loadDatabaseRows(
  id: string,
  schema: string,
  table: string,
  options: {
    limit: number;
    offset: number;
    sort?: string;
    direction?: "asc" | "desc";
  },
  signal?: AbortSignal,
): Promise<DatabaseRowsPage> {
  const params = new URLSearchParams({
    limit: String(options.limit),
    offset: String(options.offset),
  });
  if (options.sort) params.set("sort", options.sort);
  if (options.direction) params.set("direction", options.direction);
  return requestJson(
    `${tablePath(id, schema, table)}/rows?${params}`,
    rowsPageSchema,
    { signal },
  );
}

export function executeDatabaseQuery(
  id: string,
  sql: string,
  signal?: AbortSignal,
): Promise<DatabaseQueryResult> {
  return requestJson(
    `/connections/${encodePath(id)}/query`,
    queryResultSchema,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sql, args: [], limit: 500 }),
      signal,
    },
  );
}
