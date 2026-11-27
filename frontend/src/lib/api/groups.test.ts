import { afterEach, expect, it, vi } from "vitest";
import { POST } from "../../app/api/admin/access/[...path]/route";
import { createUser } from "./groups";

afterEach(() => {
  vi.unstubAllGlobals();
});

it("creates a user through the admin proxy with URL-encoded credentials", async () => {
  const created = { id: "user-1", username: "alice", active: true };
  const fetchMock = vi.fn<typeof fetch>()
    .mockImplementationOnce((url, options) => {
      const request = new Request(new URL(String(url), "http://localhost"), options);
      request.headers.set("origin", "http://localhost");
      request.headers.set("cookie", "session_id=test-session");
      return POST(request, { params: Promise.resolve({ path: ["users"] }) });
    })
    .mockResolvedValueOnce(Response.json(created, { status: 201 }));
  vi.stubGlobal("fetch", fetchMock);

  await expect(createUser("alice", "test+word& =")).resolves.toEqual(created);

  expect(fetchMock).toHaveBeenCalledTimes(2);
  const [url, request] = fetchMock.mock.calls[1];
  expect(String(url)).toMatch(/\/api\/v1\/access\/users$/);
  expect(request?.method).toBe("POST");
  const headers = new Headers(request?.headers);
  expect(headers.get("content-type")).toBe("application/x-www-form-urlencoded");
  expect(headers.get("cookie")).toBe("session_id=test-session");
  expect(headers.get("origin")).toBe("http://localhost");
  expect(await new Response(request?.body).text())
    .toBe("username=alice&password=test%2Bword%26+%3D");
});

it("surfaces the backend error when the username is already taken", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(Response.json(
    { error: { code: "username_taken", message: "username already exists" } },
    { status: 409 },
  )));

  await expect(createUser("alice", "test password")).rejects.toThrow("username already exists");
});
