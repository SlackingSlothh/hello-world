#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
PUBLIC_ORIGIN="${PUBLIC_ORIGIN:-http://localhost}"
ADMIN_USERNAME="${ADMIN_USERNAME:-admin}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-admin-local-change-me}"
RUNNER_USERNAME="${RUNNER_USERNAME:-k6_runner}"
GROUP_NAME="${GROUP_NAME:-k6-stress-group}"
PLATFORM_DB_USER="${PLATFORM_DB_USER:-hello_world}"
PLATFORM_DB_NAME="${PLATFORM_DB_NAME:-hello_world}"
EXAMPLE_DB_USER="${EXAMPLE_DB_USER:-example_user}"
FIXTURE_PATH="${FIXTURE_PATH:-k6/fixtures/stress-fixtures.json}"

PLATFORM_DB_CONTAINER="${PLATFORM_DB_CONTAINER:-$(docker compose ps -q db | head -n 1)}"
EXAMPLE_DB_CONTAINER="${EXAMPLE_DB_CONTAINER:-$(docker compose ps -q db_example | head -n 1)}"

ADMIN_COOKIE="$(mktemp)"
trap 'rm -f "${ADMIN_COOKIE}"' EXIT

if curl -fsS -c "${ADMIN_COOKIE}" -X POST "${BASE_URL}/auth/login" \
  -H "Origin: ${PUBLIC_ORIGIN}" \
  -H "Sec-Fetch-Site: same-origin" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "username=${ADMIN_USERNAME}" \
  --data-urlencode "password=${ADMIN_PASSWORD}" >/dev/null 2>&1; then
  for cid in $(curl -fsS -b "${ADMIN_COOKIE}" "${BASE_URL}/api/v1/admin/connections" | jq -r '.[] | select(.name == "k6-target-1" or .name == "k6-target-2" or .name == "k6-target-3") | .id'); do
    curl -fsS -b "${ADMIN_COOKIE}" -X DELETE "${BASE_URL}/api/v1/connections/${cid}" \
      -H "Origin: ${PUBLIC_ORIGIN}" -H "Sec-Fetch-Site: same-origin" >/dev/null
    echo "Deleted managed connection ${cid}"
  done
fi

if [[ -n "${PLATFORM_DB_CONTAINER}" ]]; then
  docker exec -i "${PLATFORM_DB_CONTAINER}" psql -U "${PLATFORM_DB_USER}" -d "${PLATFORM_DB_NAME}" -v ON_ERROR_STOP=1 <<EOF >/dev/null
DELETE FROM migration_request_approvals
WHERE request_id IN (
    SELECT r.id FROM migration_requests r
    LEFT JOIN managed_db_connections c ON c.id = r.database_id
    WHERE c.name IN ('k6-target-1', 'k6-target-2', 'k6-target-3')
       OR r.requested_by IN (SELECT id FROM platform_users WHERE username = '${RUNNER_USERNAME}')
);
DELETE FROM migration_requests
WHERE database_id IN (SELECT id FROM managed_db_connections WHERE name IN ('k6-target-1', 'k6-target-2', 'k6-target-3'))
   OR requested_by IN (SELECT id FROM platform_users WHERE username = '${RUNNER_USERNAME}');
DELETE FROM database_locks
WHERE database_id IN (SELECT id FROM managed_db_connections WHERE name IN ('k6-target-1', 'k6-target-2', 'k6-target-3'))
   OR job_id IN (SELECT job_id FROM migration_jobs WHERE actor_username = '${RUNNER_USERNAME}');
DELETE FROM migration_runs
WHERE database_id IN (SELECT id FROM managed_db_connections WHERE name IN ('k6-target-1', 'k6-target-2', 'k6-target-3'))
   OR executed_by IN (SELECT id FROM platform_users WHERE username = '${RUNNER_USERNAME}');
DELETE FROM migration_jobs
WHERE database_id IN (SELECT id FROM managed_db_connections WHERE name IN ('k6-target-1', 'k6-target-2', 'k6-target-3'))
   OR actor_username = '${RUNNER_USERNAME}';
DELETE FROM migrations
WHERE database_id IN (SELECT id FROM managed_db_connections WHERE name IN ('k6-target-1', 'k6-target-2', 'k6-target-3'))
   OR created_by IN (SELECT id FROM platform_users WHERE username = '${RUNNER_USERNAME}');
DELETE FROM connection_grants
WHERE group_id IN (SELECT id FROM access_groups WHERE name = '${GROUP_NAME}')
   OR database_id IN (SELECT id FROM managed_db_connections WHERE name IN ('k6-target-1', 'k6-target-2', 'k6-target-3'));
DELETE FROM managed_db_connections
WHERE name IN ('k6-target-1', 'k6-target-2', 'k6-target-3');
DELETE FROM managed_sessions
WHERE user_id IN (SELECT id FROM platform_users WHERE username = '${RUNNER_USERNAME}');
UPDATE access_groups SET leader_user_id = NULL WHERE name = '${GROUP_NAME}';
DELETE FROM user_group_memberships
WHERE group_id IN (SELECT id FROM access_groups WHERE name = '${GROUP_NAME}')
   OR user_id IN (SELECT id FROM platform_users WHERE username = '${RUNNER_USERNAME}');
DELETE FROM access_groups WHERE name = '${GROUP_NAME}';
DELETE FROM platform_users WHERE username = '${RUNNER_USERNAME}';
EOF
  echo "Cleaned platform database stress test records"
fi

if [[ -n "${EXAMPLE_DB_CONTAINER}" ]]; then
  for db_name in k6_stress_db_1 k6_stress_db_2 k6_stress_db_3; do
    docker exec "${EXAMPLE_DB_CONTAINER}" psql -U "${EXAMPLE_DB_USER}" -d postgres -v ON_ERROR_STOP=1 -c "DROP DATABASE IF EXISTS ${db_name} WITH (FORCE);" >/dev/null
    echo "Dropped target database ${db_name}"
  done
fi

rm -f "${FIXTURE_PATH}"
