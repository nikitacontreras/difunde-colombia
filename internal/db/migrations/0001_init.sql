-- Migración inicial: esquema principal de observaciones.
CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE IF NOT EXISTS schema_migrations (
	version   TEXT PRIMARY KEY,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS observations (
	id                   BIGSERIAL PRIMARY KEY,
	received_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
	observed_at          TIMESTAMPTZ NOT NULL,
	latitude             DOUBLE PRECISION NOT NULL,
	longitude            DOUBLE PRECISION NOT NULL,
	accuracy             DOUBLE PRECISION,
	geom                 geometry(Point, 4326),
	h3_cell              TEXT NOT NULL,
	asn                  INTEGER,
	operator             TEXT,
	mobile               BOOLEAN,
	http_rtt_min         DOUBLE PRECISION,
	http_rtt_median      DOUBLE PRECISION,
	jitter               DOUBLE PRECISION,
	success_ratio        DOUBLE PRECISION,
	samples              INTEGER,
	failed_requests      INTEGER,
	effective_type       TEXT,
	browser_rtt          DOUBLE PRECISION,
	browser_downlink     DOUBLE PRECISION,
	save_data            BOOLEAN,
	call_signal          TEXT,
	operator_user        TEXT,
	probe_1k_ms          DOUBLE PRECISION,
	probe_4k_ms          DOUBLE PRECISION,
	transfer_estimate_bps DOUBLE PRECISION
);

CREATE INDEX IF NOT EXISTS idx_observations_received_at ON observations (received_at);
CREATE INDEX IF NOT EXISTS idx_observations_observed_at ON observations (observed_at);
CREATE INDEX IF NOT EXISTS idx_observations_h3 ON observations (h3_cell);
CREATE INDEX IF NOT EXISTS idx_observations_operator ON observations (operator);
CREATE INDEX IF NOT EXISTS idx_observations_asn ON observations (asn);
CREATE INDEX IF NOT EXISTS idx_observations_geom ON observations USING GIST (geom);
