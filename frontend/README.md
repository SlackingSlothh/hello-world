# Frontend

Run the full stack with Docker Compose from the [root README](../README.md).
The frontend uses Next.js App Router. Routes live in `src/app/`, reusable UI in
`src/components/`, and API clients in `src/lib/api/`.

- `/admin` manages connections, groups, and grants through the backend.
- `/migration` lists accessible connections and reads their migration history from the backend.
- `/db` displays mock schema and query data from `src/lib/mock-data.ts`.

`src/proxy.ts` checks sessions and admin access through the backend before
rendering protected pages. API proxies use `INTERNAL_API_BASE_URL`. Keep
user-specific responses out of shared caches.

## Checks

Run from `frontend/` with Node.js 24.

```sh
npm ci
npm test
npm run lint
```
