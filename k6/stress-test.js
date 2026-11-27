import http from 'k6/http';
import { check, sleep } from 'k6';
import exec from 'k6/execution';
import { Counter, Rate, Trend } from 'k6/metrics';

const BASE_URL = __ENV.BASE_URL || 'http://app:8080';
const PUBLIC_ORIGIN = __ENV.PUBLIC_ORIGIN || 'http://localhost';
const FIXTURES_PATH = __ENV.FIXTURES_PATH || './fixtures/stress-fixtures.json';
const SUMMARY_DIR = __ENV.SUMMARY_DIR || '/scripts/results';

const fixtures = JSON.parse(open(FIXTURES_PATH).replace(/^\uFEFF/, ''));

const STAGE_BOUNDS = [
  { name: 'normal', startSec: 0, endSec: 50, durationSec: 50 },
  { name: 'mid', startSec: 50, endSec: 100, durationSec: 50 },
  { name: 'peak', startSec: 100, endSec: 160, durationSec: 60 },
  { name: 'recovery', startSec: 160, endSec: 210, durationSec: 50 },
];

const readApiRequests = new Counter('read_api_requests_total');
const jobQueueWaitMs = new Trend('job_queue_wait_ms', true);
const jobExecDurationMs = new Trend('job_exec_duration_ms', true);
const jobE2eDurationMs = new Trend('job_e2e_duration_ms', true);
const jobsCompletedTotal = new Counter('jobs_completed_total');
const jobFailedRate = new Rate('job_failed_rate');

function buildThresholds() {
  const thresholds = {
    'http_req_duration{scenario:read_api}': ['p(95)<500'],
    'http_req_failed': ['rate<0.05'],
    'job_failed_rate': ['rate<0.05'],
    'job_queue_wait_ms{db_group:same_db}': ['p(95)>=0'],
    'job_queue_wait_ms{db_group:diff_db}': ['p(95)>=0'],
    'job_exec_duration_ms{db_group:same_db}': ['p(95)>=0'],
    'job_exec_duration_ms{db_group:diff_db}': ['p(95)>=0'],
    'job_e2e_duration_ms{db_group:same_db}': ['p(95)>=0'],
    'job_e2e_duration_ms{db_group:diff_db}': ['p(95)>=0'],
  };
  for (const s of STAGE_BOUNDS) {
    const st = s.name;
    thresholds[`http_req_duration{scenario:read_api,stage:${st}}`] = ['p(95)>=0'];
    thresholds[`http_req_failed{scenario:read_api,stage:${st}}`] = ['rate>=0'];
    thresholds[`read_api_requests_total{stage:${st}}`] = ['count>=0'];
    thresholds[`job_queue_wait_ms{stage:${st}}`] = ['p(95)>=0'];
    thresholds[`job_queue_wait_ms{stage:${st},db_group:same_db}`] = ['p(95)>=0'];
    thresholds[`job_queue_wait_ms{stage:${st},db_group:diff_db}`] = ['p(95)>=0'];
    thresholds[`job_exec_duration_ms{stage:${st}}`] = ['p(95)>=0'];
    thresholds[`job_e2e_duration_ms{stage:${st}}`] = ['p(95)>=0'];
    thresholds[`jobs_completed_total{stage:${st}}`] = ['count>=0'];
    thresholds[`job_failed_rate{stage:${st}}`] = ['rate>=0'];
  }
  return thresholds;
}

export const options = {
  summaryTrendStats: ['min', 'avg', 'p(50)', 'p(95)', 'max'],
  thresholds: buildThresholds(),
  scenarios: {
    read_api_traffic: {
      executor: 'ramping-vus',
      exec: 'readApiScenario',
      startVUs: 0,
      gracefulRampDown: '10s',
      gracefulStop: '15s',
      stages: [
        { duration: '10s', target: 8 },
        { duration: '40s', target: 8 },
        { duration: '10s', target: 24 },
        { duration: '40s', target: 24 },
        { duration: '15s', target: 64 },
        { duration: '45s', target: 64 },
        { duration: '10s', target: 8 },
        { duration: '40s', target: 8 },
        { duration: '10s', target: 0 },
      ],
    },
    migration_jobs_traffic: {
      executor: 'ramping-vus',
      exec: 'migrationJobsScenario',
      startVUs: 0,
      gracefulRampDown: '20s',
      gracefulStop: '45s',
      stages: [
        { duration: '10s', target: 2 },
        { duration: '40s', target: 2 },
        { duration: '10s', target: 4 },
        { duration: '40s', target: 4 },
        { duration: '15s', target: 8 },
        { duration: '45s', target: 8 },
        { duration: '10s', target: 2 },
        { duration: '40s', target: 2 },
        { duration: '10s', target: 0 },
      ],
    },
  },
};

const TERMINAL_STATUSES = {
  success: true,
  failed: true,
  partial: true,
  partially_reverted: true,
  uncertain: true,
};

let vuSessionCookie = '';

export function setup() {
  return {
    startMs: Date.now(),
    startIso: new Date().toISOString(),
  };
}

function getStage(startMs) {
  const elapsedSec = (Date.now() - startMs) / 1000;
  for (const b of STAGE_BOUNDS) {
    if (elapsedSec < b.endSec) {
      return b.name;
    }
  }
  return 'recovery';
}

function ensureVuLogin(stage) {
  if (vuSessionCookie) {
    return true;
  }
  const body = `username=${encodeURIComponent(fixtures.runner_username)}&password=${encodeURIComponent(fixtures.runner_password)}`;
  const res = http.post(`${BASE_URL}/auth/login`, body, {
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
      'Origin': PUBLIC_ORIGIN,
      'Sec-Fetch-Site': 'same-origin',
    },
    tags: { scenario: 'auth', endpoint: 'auth_login', stage },
  });
  const ok = check(res, {
    'login status 204': (r) => r.status === 204,
  });
  if (ok && res.cookies && res.cookies.session_id && res.cookies.session_id.length > 0) {
    vuSessionCookie = `session_id=${res.cookies.session_id[0].value}`;
    http.cookieJar().clear(BASE_URL);
  }
  return Boolean(vuSessionCookie);
}

export function readApiScenario(data) {
  const stage = getStage(data.startMs);
  if (!ensureVuLogin(stage)) {
    sleep(1);
    return;
  }

  const conns = fixtures.connections;
  const target = conns[(exec.vu.idInTest + exec.vu.iterationInScenario) % conns.length];
  const cid = encodeURIComponent(target.id);
  const endpoints = [
    { name: 'list_connections', url: `${BASE_URL}/api/v1/connections` },
    { name: 'list_schemas', url: `${BASE_URL}/api/v1/connections/${cid}/schemas` },
    { name: 'table_rows', url: `${BASE_URL}/api/v1/connections/${cid}/schemas/public/tables/k6_stress_target/rows?limit=20&offset=0` },
    { name: 'migration_history', url: `${BASE_URL}/api/v1/connections/${cid}/migrations/history` },
  ];

  const picked = endpoints[exec.vu.iterationInScenario % endpoints.length];
  const res = http.get(picked.url, {
    headers: {
      'Cookie': vuSessionCookie,
    },
    tags: {
      scenario: 'read_api',
      stage,
      endpoint: picked.name,
      db_group: target.db_group,
    },
  });
  readApiRequests.add(1, { stage });
  check(res, {
    'read api status 200': (r) => r.status === 200,
  });
  sleep(0.25);
}

export function migrationJobsScenario(data) {
  const stage = getStage(data.startMs);
  if (!ensureVuLogin(stage)) {
    sleep(1);
    return;
  }

  const idx = exec.scenario.iterationInTest;
  if (idx >= fixtures.requests.length) {
    sleep(1);
    return;
  }

  const item = fixtures.requests[idx];
  const cid = encodeURIComponent(item.connection_id);
  const metricTags = { stage, db_group: item.db_group };
  const clientStartMs = Date.now();

  const enqueueRes = http.post(
    `${BASE_URL}/api/v1/connections/${cid}/migration-requests/${item.request_id}/execute`,
    null,
    {
      headers: {
        'Cookie': vuSessionCookie,
        'Origin': PUBLIC_ORIGIN,
        'Sec-Fetch-Site': 'same-origin',
      },
      tags: {
        scenario: 'migration_jobs',
        endpoint: 'enqueue_request',
        stage,
        db_group: item.db_group,
      },
    },
  );

  const enqueueOk = check(enqueueRes, {
    'enqueue accepted 202': (r) => r.status === 202,
  });
  if (!enqueueOk) {
    jobFailedRate.add(true, metricTags);
    sleep(1);
    return;
  }

  let jobId = '';
  try {
    const parsed = enqueueRes.json();
    jobId = parsed && parsed.job_id ? parsed.job_id : '';
  } catch (_) {
    jobId = '';
  }
  if (!jobId) {
    jobFailedRate.add(true, metricTags);
    sleep(1);
    return;
  }

  const pollDeadlineMs = Date.now() + 60000;
  let finalJob = null;

  while (Date.now() < pollDeadlineMs) {
    sleep(0.25);
    const pollRes = http.get(`${BASE_URL}/api/v1/jobs/${encodeURIComponent(jobId)}`, {
      headers: {
        'Cookie': vuSessionCookie,
      },
      tags: {
        scenario: 'migration_jobs',
        endpoint: 'poll_job',
        stage,
        db_group: item.db_group,
      },
    });
    if (pollRes.status !== 200) {
      continue;
    }
    let jobBody = null;
    try {
      jobBody = pollRes.json();
    } catch (_) {
      jobBody = null;
    }
    if (jobBody && TERMINAL_STATUSES[jobBody.status]) {
      finalJob = jobBody;
      break;
    }
  }

  const clientEndMs = Date.now();
  if (!finalJob) {
    jobFailedRate.add(true, metricTags);
    return;
  }

  const succeeded = finalJob.status === 'success';
  jobFailedRate.add(!succeeded, metricTags);
  if (succeeded) {
    jobsCompletedTotal.add(1, metricTags);
  }

  const enqueuedMs = finalJob.enqueued_at ? Date.parse(finalJob.enqueued_at) : clientStartMs;
  const startedMs = finalJob.started_at ? Date.parse(finalJob.started_at) : enqueuedMs;
  const finishedMs = finalJob.finished_at ? Date.parse(finalJob.finished_at) : clientEndMs;

  const queueWait = Math.max(0, startedMs - enqueuedMs);
  const execDur = Math.max(0, finishedMs - startedMs);
  const e2eDur = Math.max(0, finishedMs - enqueuedMs);

  jobQueueWaitMs.add(queueWait, metricTags);
  jobExecDurationMs.add(execDur, metricTags);
  jobE2eDurationMs.add(e2eDur, metricTags);

  if (stage === 'peak') {
    sleep(0.55);
  } else if (stage === 'mid') {
    sleep(1.6);
  } else if (stage === 'recovery') {
    sleep(2.0);
  } else {
    sleep(2.5);
  }
}

function metricVal(metrics, key, stat) {
  const m = metrics[key];
  if (!m || !m.values || m.values[stat] === undefined || m.values[stat] === null) {
    return null;
  }
  return Number(m.values[stat]);
}

function fmt(val, digits = 2) {
  if (val === null || val === undefined || Number.isNaN(val)) {
    return 'N/A';
  }
  return Number(val).toFixed(digits);
}

export function handleSummary(data) {
  const durationMs = (data.state && data.state.testRunDurationMs) ? data.state.testRunDurationMs : 220000;
  const endDate = new Date();
  const startDate = new Date(endDate.getTime() - durationMs);
  const metrics = data.metrics || {};

  const stageWindows = {};
  const stageReport = {};

  for (const b of STAGE_BOUNDS) {
    const st = b.name;
    const stStart = new Date(startDate.getTime() + b.startSec * 1000);
    const stEnd = new Date(Math.min(endDate.getTime(), startDate.getTime() + b.endSec * 1000));
    stageWindows[st] = {
      start_iso: stStart.toISOString(),
      end_iso: stEnd.toISOString(),
      start_unix: Math.floor(stStart.getTime() / 1000),
      end_unix: Math.floor(stEnd.getTime() / 1000),
      duration_sec: b.durationSec,
    };

    const readCount = metricVal(metrics, `read_api_requests_total{stage:${st}}`, 'count') || 0;
    const jobsCount = metricVal(metrics, `jobs_completed_total{stage:${st}}`, 'count') || 0;

    stageReport[st] = {
      read_api_requests: readCount,
      read_api_rps: Number((readCount / b.durationSec).toFixed(2)),
      read_api_p50_ms: metricVal(metrics, `http_req_duration{scenario:read_api,stage:${st}}`, 'p(50)'),
      read_api_p95_ms: metricVal(metrics, `http_req_duration{scenario:read_api,stage:${st}}`, 'p(95)'),
      read_api_avg_ms: metricVal(metrics, `http_req_duration{scenario:read_api,stage:${st}}`, 'avg'),
      read_api_max_ms: metricVal(metrics, `http_req_duration{scenario:read_api,stage:${st}}`, 'max'),
      read_api_error_rate: metricVal(metrics, `http_req_failed{scenario:read_api,stage:${st}}`, 'rate'),
      jobs_completed: jobsCount,
      jobs_per_min: Number(((jobsCount / b.durationSec) * 60).toFixed(2)),
      job_failed_rate: metricVal(metrics, `job_failed_rate{stage:${st}}`, 'rate'),
      job_queue_wait_p50_ms: metricVal(metrics, `job_queue_wait_ms{stage:${st}}`, 'p(50)'),
      job_queue_wait_p95_ms: metricVal(metrics, `job_queue_wait_ms{stage:${st}}`, 'p(95)'),
      job_queue_wait_same_db_p50_ms: metricVal(metrics, `job_queue_wait_ms{stage:${st},db_group:same_db}`, 'p(50)'),
      job_queue_wait_same_db_p95_ms: metricVal(metrics, `job_queue_wait_ms{stage:${st},db_group:same_db}`, 'p(95)'),
      job_queue_wait_diff_db_p50_ms: metricVal(metrics, `job_queue_wait_ms{stage:${st},db_group:diff_db}`, 'p(50)'),
      job_queue_wait_diff_db_p95_ms: metricVal(metrics, `job_queue_wait_ms{stage:${st},db_group:diff_db}`, 'p(95)'),
      job_exec_p50_ms: metricVal(metrics, `job_exec_duration_ms{stage:${st}}`, 'p(50)'),
      job_exec_p95_ms: metricVal(metrics, `job_exec_duration_ms{stage:${st}}`, 'p(95)'),
      job_e2e_p50_ms: metricVal(metrics, `job_e2e_duration_ms{stage:${st}}`, 'p(50)'),
      job_e2e_p95_ms: metricVal(metrics, `job_e2e_duration_ms{stage:${st}}`, 'p(95)'),
    };
  }

  const thresholdResults = {};
  for (const [k, m] of Object.entries(metrics)) {
    if (m.thresholds) {
      thresholdResults[k] = m.thresholds;
    }
  }

  const summary = {
    test_window_utc: {
      start_iso: startDate.toISOString(),
      end_iso: endDate.toISOString(),
      start_unix: Math.floor(startDate.getTime() / 1000),
      end_unix: Math.floor(endDate.getTime() / 1000),
      duration_ms: Math.round(durationMs),
    },
    stage_windows_utc: stageWindows,
    overall: {
      http_reqs: metricVal(metrics, 'http_reqs', 'count'),
      http_rps: metricVal(metrics, 'http_reqs', 'rate'),
      http_req_failed_rate: metricVal(metrics, 'http_req_failed', 'rate'),
      read_api_p50_ms: metricVal(metrics, 'http_req_duration{scenario:read_api}', 'p(50)'),
      read_api_p95_ms: metricVal(metrics, 'http_req_duration{scenario:read_api}', 'p(95)'),
      read_api_avg_ms: metricVal(metrics, 'http_req_duration{scenario:read_api}', 'avg'),
      read_api_max_ms: metricVal(metrics, 'http_req_duration{scenario:read_api}', 'max'),
      jobs_completed: metricVal(metrics, 'jobs_completed_total', 'count'),
      job_failed_rate: metricVal(metrics, 'job_failed_rate', 'rate'),
      job_queue_wait_same_db_p50_ms: metricVal(metrics, 'job_queue_wait_ms{db_group:same_db}', 'p(50)'),
      job_queue_wait_same_db_p95_ms: metricVal(metrics, 'job_queue_wait_ms{db_group:same_db}', 'p(95)'),
      job_queue_wait_diff_db_p50_ms: metricVal(metrics, 'job_queue_wait_ms{db_group:diff_db}', 'p(50)'),
      job_queue_wait_diff_db_p95_ms: metricVal(metrics, 'job_queue_wait_ms{db_group:diff_db}', 'p(95)'),
      job_exec_p50_ms: metricVal(metrics, 'job_exec_duration_ms', 'p(50)'),
      job_exec_p95_ms: metricVal(metrics, 'job_exec_duration_ms', 'p(95)'),
      job_e2e_p50_ms: metricVal(metrics, 'job_e2e_duration_ms', 'p(50)'),
      job_e2e_p95_ms: metricVal(metrics, 'job_e2e_duration_ms', 'p(95)'),
    },
    stages: stageReport,
    thresholds: thresholdResults,
  };

  const lines = [
    '=== DataDock k6 Stress Test Summary ===',
    `Window UTC : ${summary.test_window_utc.start_iso} -> ${summary.test_window_utc.end_iso} (${summary.test_window_utc.duration_ms} ms)`,
    `Overall HTTP: reqs=${fmt(summary.overall.http_reqs, 0)} rps=${fmt(summary.overall.http_rps)} err=${fmt((summary.overall.http_req_failed_rate || 0) * 100)}% read_p50=${fmt(summary.overall.read_api_p50_ms)}ms read_p95=${fmt(summary.overall.read_api_p95_ms)}ms`,
    `Overall Jobs: completed=${fmt(summary.overall.jobs_completed, 0)} fail=${fmt((summary.overall.job_failed_rate || 0) * 100)}% queue_same_p95=${fmt(summary.overall.job_queue_wait_same_db_p95_ms)}ms queue_diff_p95=${fmt(summary.overall.job_queue_wait_diff_db_p95_ms)}ms exec_p95=${fmt(summary.overall.job_exec_p95_ms)}ms e2e_p95=${fmt(summary.overall.job_e2e_p95_ms)}ms`,
    '',
    'Stage      | Read RPS | Read p50 | Read p95 | Read Err% | Jobs | Jobs/m | Job Err% | QWait Same p95 | QWait Diff p95 | Exec p95 | E2E p95',
    '-----------|----------|----------|----------|-----------|------|--------|----------|----------------|----------------|----------|--------',
  ];

  for (const b of STAGE_BOUNDS) {
    const r = stageReport[b.name];
    lines.push(
      `${b.name.padEnd(10)} | ${fmt(r.read_api_rps).padStart(8)} | ${fmt(r.read_api_p50_ms).padStart(8)} | ${fmt(r.read_api_p95_ms).padStart(8)} | ${fmt((r.read_api_error_rate || 0) * 100).padStart(9)} | ${fmt(r.jobs_completed, 0).padStart(4)} | ${fmt(r.jobs_per_min).padStart(6)} | ${fmt((r.job_failed_rate || 0) * 100).padStart(8)} | ${fmt(r.job_queue_wait_same_db_p95_ms).padStart(14)} | ${fmt(r.job_queue_wait_diff_db_p95_ms).padStart(14)} | ${fmt(r.job_exec_p95_ms).padStart(8)} | ${fmt(r.job_e2e_p95_ms).padStart(7)}`,
    );
  }

  const textOutput = lines.join('\n') + '\n';
  return {
    stdout: textOutput,
    [`${SUMMARY_DIR}/summary.json`]: JSON.stringify(summary, null, 2) + '\n',
    [`${SUMMARY_DIR}/summary.txt`]: textOutput,
  };
}
