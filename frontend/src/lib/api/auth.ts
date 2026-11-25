export type LoginCredentials = {
  username: string;
  password: string;
};

async function postAuthAction(
  action: "login" | "logout",
  values?: LoginCredentials,
): Promise<void> {
  let response: Response;
  try {
    response = await fetch(`/api/auth/${action}`, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams(values ?? {}),
      cache: "no-store",
    });
  } catch (error) {
    if (error instanceof TypeError) {
      throw new Error("Could not reach the authentication service.");
    }
    throw error;
  }

  if (response.ok) return;

  if (response.status === 401) {
    throw new Error("Username or password is incorrect.");
  }
  if (response.status === 403) {
    throw new Error(
      "The request was rejected. Check the frontend origin configured on the backend.",
    );
  }
  if (response.status >= 500) {
    throw new Error("The authentication service is unavailable.");
  }
  throw new Error(`Authentication failed (HTTP ${response.status}).`);
}

export function login(credentials: LoginCredentials): Promise<void> {
  return postAuthAction("login", credentials);
}

export function logout(): Promise<void> {
  return postAuthAction("logout");
}
