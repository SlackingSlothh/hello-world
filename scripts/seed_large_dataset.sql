DROP TABLE IF EXISTS public.benchmark_records;

CREATE TABLE public.benchmark_records (
    id BIGINT PRIMARY KEY,
    account_code TEXT NOT NULL,
    status TEXT NOT NULL,
    payload TEXT NOT NULL,
    checksum TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO public.benchmark_records (id, account_code, status, payload, checksum, updated_at)
SELECT
    g AS id,
    'ACCT-' || lpad((g % 10000)::text, 5, '0') AS account_code,
    'pending' AS status,
    repeat(md5(g::text), 6) AS payload,
    '' AS checksum,
    NOW() AS updated_at
FROM generate_series(1, 500000) AS g;

CREATE INDEX idx_benchmark_records_status ON public.benchmark_records (status);
CREATE INDEX idx_benchmark_records_account ON public.benchmark_records (account_code);

ANALYZE public.benchmark_records;
