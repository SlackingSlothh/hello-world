DROP TABLE IF EXISTS public.k6_stress_target;

CREATE TABLE public.k6_stress_target (
    id INT PRIMARY KEY,
    bucket INT NOT NULL,
    counter INT NOT NULL DEFAULT 0,
    payload TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO public.k6_stress_target (id, bucket, counter, payload, updated_at)
SELECT
    g AS id,
    (g % 20) AS bucket,
    0 AS counter,
    repeat(md5(g::text), 6) AS payload,
    NOW() AS updated_at
FROM generate_series(1, 10000) AS g;

CREATE INDEX idx_k6_stress_target_bucket ON public.k6_stress_target (bucket);
CREATE INDEX idx_k6_stress_target_counter ON public.k6_stress_target (counter);

ANALYZE public.k6_stress_target;
