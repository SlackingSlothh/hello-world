#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
PUBLIC_ORIGIN="${PUBLIC_ORIGIN:-http://localhost}"
ADMIN_USERNAME="${ADMIN_USERNAME:-admin}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-admin-local-change-me}"
RUNNER_USERNAME="${RUNNER_USERNAME:-k6_runner}"
RUNNER_PASSWORD="${RUNNER_PASSWORD:-k6-stress-runner-password}"
GROUP_NAME="${GROUP_NAME:-k6-stress-group}"
EXAMPLE_DB_USER="${EXAMPLE_DB_USER:-example_user}"
EXAMPLE_DB_PASSWORD="${EXAMPLE_DB_PASSWORD:-example_local_password}"
EXAMPLE_DB_HOST="${EXAMPLE_DB_HOST:-db_example}"
SAME_DB_COUNT="${SAME_DB_COUNT:-240}"
DIFF_DB1_COUNT="${DIFF_DB1_COUNT:-80}"
DIFF_DB2_COUNT="${DIFF_DB2_COUNT:-80}"
OUTPUT_PATH="${OUTPUT_PATH:-k6/fixtures/stress-fixtures.json}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INIT_SQL="${SCRIPT_DIR}/sql/init_target_db.sql"

EXAMPLE_DB_CONTAINER="${EXAMPLE_DB_CONTAINER:-$(docker compose ps -q db_example | head -n 1)}"
if [[ -z "${EXAMPLE_DB_CONTAINER}" ]]; then
  echo "db_example container is not running" >&2
  exit 1
fi

for db_name in k6_stress_db_1 k6_stress_db_2 k6_stress_db_3; do
  exists="$(docker exec "${EXAMPLE_DB_CONTAINER}" psql -U "${EXAMPLE_DB_USER}" -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname = '${db_name}';" | tr -d '[:space:]')"
  if [[ "${exists}" != "1" ]]; then
    docker exec "${EXAMPLE_DB_CONTAINER}" psql -U "${EXAMPLE_DB_USER}" -d postgres -v ON_ERROR_STOP=1 -c "CREATE DATABASE ${db_name};" >/dev/null
  fi
  docker exec -i "${EXAMPLE_DB_CONTAINER}" psql -U "${EXAMPLE_DB_USER}" -d "${db_name}" -v ON_ERROR_STOP=1 < "${INIT_SQL}" >/dev/null
  echo "Initialized target database ${db_name} (10000 rows)"
done

ADMIN_COOKIE="$(mktemp)"
RUNNER_COOKIE="$(mktemp)"
trap 'rm -f "${ADMIN_COOKIE}" "${RUNNER_COOKIE}"' EXIT

curl -fsS -c "${ADMIN_COOKIE}" -X POST "${BASE_URL}/auth/login" \
  -H "Origin: ${PUBLIC_ORIGIN}" \
  -H "Sec-Fetch-Site: same-origin" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "username=${ADMIN_USERNAME}" \
  --data-urlencode "password=${ADMIN_PASSWORD}" >/dev/null

RUNNER_ID="$(curl -fsS -b "${ADMIN_COOKIE}" "${BASE_URL}/api/v1/access/users" | jq -r --arg u "${RUNNER_USERNAME}" '.[] | select(.username == $u) | .id' | head -n 1)"
if [[ -z "${RUNNER_ID}" ]]; then
  RUNNER_ID="$(curl -fsS -b "${ADMIN_COOKIE}" -X POST "${BASE_URL}/api/v1/access/users" \
    -H "Origin: ${PUBLIC_ORIGIN}" \
    -H "Sec-Fetch-Site: same-origin" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    --data-urlencode "username=${RUNNER_USERNAME}" \
    --data-urlencode "password=${RUNNER_PASSWORD}" | jq -r '.id')"
fi

GROUP_ID="$(curl -fsS -b "${ADMIN_COOKIE}" "${BASE_URL}/api/v1/access/groups" | jq -r --arg g "${GROUP_NAME}" '.groups[] | select(.name == $g) | .id' | head -n 1)"
if [[ -z "${GROUP_ID}" ]]; then
  GROUP_ID="$(curl -fsS -b "${ADMIN_COOKIE}" -X POST "${BASE_URL}/api/v1/access/groups" \
    -H "Origin: ${PUBLIC_ORIGIN}" \
    -H "Sec-Fetch-Site: same-origin" \
    -H "Content-Type: application/json" \
    -d "$(jq -nc --arg name "${GROUP_NAME}" --arg uid "${RUNNER_ID}" '{name:$name,memberIds:[$uid],leaderId:$uid}')" | jq -r '.id')"
else
  curl -fsS -b "${ADMIN_COOKIE}" -X PUT "${BASE_URL}/api/v1/access/groups/${GROUP_ID}/members/${RUNNER_ID}" \
    -H "Origin: ${PUBLIC_ORIGIN}" -H "Sec-Fetch-Site: same-origin" >/dev/null
  curl -fsS -b "${ADMIN_COOKIE}" -X PUT "${BASE_URL}/api/v1/access/groups/${GROUP_ID}/leader" \
    -H "Origin: ${PUBLIC_ORIGIN}" -H "Sec-Fetch-Site: same-origin" -H "Content-Type: application/json" \
    -d "$(jq -nc --arg uid "${RUNNER_ID}" '{leaderId:$uid}')" >/dev/null
fi

EXISTING_CONNS="$(curl -fsS -b "${ADMIN_COOKIE}" "${BASE_URL}/api/v1/admin/connections")"

ensure_conn() {
  local name="$1"
  local db_name="$2"
  local cid
  cid="$(printf '%s' "${EXISTING_CONNS}" | jq -r --arg n "${name}" '.[] | select(.name == $n) | .id' | head -n 1)"
  if [[ -z "${cid}" ]]; then
    local dsn="postgres://${EXAMPLE_DB_USER}:${EXAMPLE_DB_PASSWORD}@${EXAMPLE_DB_HOST}:5432/${db_name}?sslmode=disable"
    cid="$(curl -fsS -b "${ADMIN_COOKIE}" -X POST "${BASE_URL}/api/v1/connections" \
      -H "Origin: ${PUBLIC_ORIGIN}" -H "Sec-Fetch-Site: same-origin" -H "Content-Type: application/json" \
      -d "$(jq -nc --arg name "${name}" --arg dsn "${dsn}" '{name:$name,driver:"postgres",dsn:$dsn,pool:{maxOpen:10,maxIdle:4,maxLifetimeSeconds:300,maxIdleTimeSeconds:60}}')" | jq -r '.id')"
  fi
  curl -fsS -b "${ADMIN_COOKIE}" -X PUT "${BASE_URL}/api/v1/access/groups/${GROUP_ID}/connections/${cid}" \
    -H "Origin: ${PUBLIC_ORIGIN}" -H "Sec-Fetch-Site: same-origin" -H "Content-Type: application/json" \
    -d '{"access":"write"}' >/dev/null
  printf '%s' "${cid}"
}

CONN1="$(ensure_conn k6-target-1 k6_stress_db_1)"
CONN2="$(ensure_conn k6-target-2 k6_stress_db_2)"
CONN3="$(ensure_conn k6-target-3 k6_stress_db_3)"

curl -fsS -c "${RUNNER_COOKIE}" -X POST "${BASE_URL}/auth/login" \
  -H "Origin: ${PUBLIC_ORIGIN}" \
  -H "Sec-Fetch-Site: same-origin" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "username=${RUNNER_USERNAME}" \
  --data-urlencode "password=${RUNNER_PASSWORD}" >/dev/null

SAME_FILE="$(mktemp)"
DIFF_FILE="$(mktemp)"
trap 'rm -f "${ADMIN_COOKIE}" "${RUNNER_COOKIE}" "${SAME_FILE}" "${DIFF_FILE}"' EXIT

seed_requests() {
  local conn_id="$1"
  local conn_name="$2"
  local db_group="$3"
  local count="$4"
  local target_file="$5"
  for ((i = 1; i <= count; i++)); do
    local bucket=$((i % 20))
    local sql_up="UPDATE public.k6_stress_target SET counter = counter + 1, payload = md5(payload || id::text), updated_at = NOW();"
    local sql_down="UPDATE public.k6_stress_target SET counter = GREATEST(counter - 1, 0), updated_at = NOW() WHERE bucket = ${bucket};"
    local draft
    draft="$(curl -fsS -b "${RUNNER_COOKIE}" -X POST "${BASE_URL}/api/v1/connections/${conn_id}/migrations" \
      -H "Origin: ${PUBLIC_ORIGIN}" -H "Sec-Fetch-Site: same-origin" -H "Content-Type: application/json" \
      -d "$(jq -nc --arg t "k6 stress ${conn_name} #${i}" --arg d "k6 stress test apply request ${i}" --arg up "${sql_up}" --arg down "${sql_down}" '{title:$t,description:$d,sql_content:$up,sql_down:$down,execution_mode:"normal"}')")"
    local mid rev req rid
    mid="$(printf '%s' "${draft}" | jq -r '.id')"
    rev="$(printf '%s' "${draft}" | jq -r '.revision')"
    req="$(curl -fsS -b "${RUNNER_COOKIE}" -X POST "${BASE_URL}/api/v1/connections/${conn_id}/migrations/${mid}/requests" \
      -H "Origin: ${PUBLIC_ORIGIN}" -H "Sec-Fetch-Site: same-origin" -H "Content-Type: application/json" \
      -d "$(jq -nc --argjson rev "${rev}" '{operation:"APPLY",revision:$rev}')")"
    rid="$(printf '%s' "${req}" | jq -r '.id')"
    curl -fsS -b "${RUNNER_COOKIE}" -X PUT "${BASE_URL}/api/v1/connections/${conn_id}/migration-requests/${rid}/approvals/${GROUP_ID}" \
      -H "Origin: ${PUBLIC_ORIGIN}" -H "Sec-Fetch-Site: same-origin" >/dev/null
    jq -nc --arg cid "${conn_id}" --arg cname "${conn_name}" --arg grp "${db_group}" --argjson mid "${mid}" --argjson rid "${rid}" \
      '{connection_id:$cid,connection_name:$cname,db_group:$grp,migration_id:$mid,request_id:$rid}' >> "${target_file}"
  done
}

seed_requests "${CONN1}" "k6-target-1" "same_db" "${SAME_DB_COUNT}" "${SAME_FILE}"
seed_requests "${CONN2}" "k6-target-2" "diff_db" "${DIFF_DB1_COUNT}" "${DIFF_FILE}"
seed_requests "${CONN3}" "k6-target-3" "diff_db" "${DIFF_DB2_COUNT}" "${DIFF_FILE}"

mkdir -p "$(dirname "${OUTPUT_PATH}")"

jq -n \
  --arg generated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg public_origin "${PUBLIC_ORIGIN}" \
  --arg runner_username "${RUNNER_USERNAME}" \
  --arg runner_password "${RUNNER_PASSWORD}" \
  --arg group_id "${GROUP_ID}" \
  --arg group_name "${GROUP_NAME}" \
  --arg c1 "${CONN1}" --arg c2 "${CONN2}" --arg c3 "${CONN3}" \
  --slurpfile same "${SAME_FILE}" \
  --slurpfile diff "${DIFF_FILE}" '
  def interleave($a; $b):
    [range(0; (([$a|length/3, $b|length/2] | max) | ceil)) as $k |
      ($a[$k*3 : $k*3+3][]),
      ($b[$k*2 : $k*2+2][])
    ];
  {
    generated_at: $generated_at,
    public_origin: $public_origin,
    runner_username: $runner_username,
    runner_password: $runner_password,
    group_id: $group_id,
    group_name: $group_name,
    connections: [
      {id:$c1,name:"k6-target-1",db_name:"k6_stress_db_1",db_group:"same_db"},
      {id:$c2,name:"k6-target-2",db_name:"k6_stress_db_2",db_group:"diff_db"},
      {id:$c3,name:"k6-target-3",db_name:"k6_stress_db_3",db_group:"diff_db"}
    ],
    requests: interleave($same; $diff)
  }' > "${OUTPUT_PATH}"

echo "Wrote stress test fixtures to ${OUTPUT_PATH}"
