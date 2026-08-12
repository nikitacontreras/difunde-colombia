-- Datos oficiales (baseline) e infraestructura importada
-- desde scrape_telco_colombia.py. No se inventan datos ausentes:
-- las tablas reflejan lo que los datasets realmente soportan.

CREATE TABLE IF NOT EXISTS official_coverage (
	id            BIGSERIAL PRIMARY KEY,
	source        TEXT NOT NULL,
	source_date   TEXT,
	imported_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
	year          INTEGER,
	quarter       INTEGER,
	dane_code     INTEGER,
	municipality  TEXT,
	technology    TEXT,
	signal_level  INTEGER,
	area_km2      DOUBLE PRECISION,
	area_pct_claro    DOUBLE PRECISION,
	area_pct_movistar DOUBLE PRECISION,
	area_pct_tigo     DOUBLE PRECISION,
	area_pct_wom      DOUBLE PRECISION
);
CREATE INDEX IF NOT EXISTS idx_official_coverage_dane ON official_coverage (dane_code);
CREATE INDEX IF NOT EXISTS idx_official_coverage_tech ON official_coverage (technology);

CREATE TABLE IF NOT EXISTS official_sites (
	id           BIGSERIAL PRIMARY KEY,
	source       TEXT NOT NULL,
	source_date  TEXT,
	imported_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
	year         INTEGER,
	quarter      INTEGER,
	dane_code    INTEGER,
	municipality TEXT,
	operator     TEXT,
	sitios       INTEGER,
	own_co_location INTEGER,
	tech_2g BOOLEAN NOT NULL DEFAULT false,
	tech_3g BOOLEAN NOT NULL DEFAULT false,
	tech_4g BOOLEAN NOT NULL DEFAULT false,
	tech_5g BOOLEAN NOT NULL DEFAULT false
);
CREATE INDEX IF NOT EXISTS idx_official_sites_dane ON official_sites (dane_code);

CREATE TABLE IF NOT EXISTS mobile_sites (
	id            BIGSERIAL PRIMARY KEY,
	source        TEXT NOT NULL,
	source_date   TEXT,
	imported_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
	latitude      DOUBLE PRECISION NOT NULL,
	longitude     DOUBLE PRECISION NOT NULL,
	geom          geometry(Point, 4326),
	h3_cell       TEXT,
	operator      TEXT,
	operator_raw  TEXT,
	mobile        BOOLEAN NOT NULL DEFAULT true,
	address       TEXT,
	neighborhood  TEXT
);
CREATE INDEX IF NOT EXISTS idx_mobile_sites_geom ON mobile_sites USING GIST (geom);
CREATE INDEX IF NOT EXISTS idx_mobile_sites_h3 ON mobile_sites (h3_cell);
CREATE INDEX IF NOT EXISTS idx_mobile_sites_operator ON mobile_sites (operator);

CREATE TABLE IF NOT EXISTS asn_operator_mapping (
	asn         INTEGER PRIMARY KEY,
	operator    TEXT NOT NULL,
	mobile      BOOLEAN NOT NULL DEFAULT false,
	confidence  DOUBLE PRECISION NOT NULL DEFAULT 0.5,
	source      TEXT,
	imported_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS resources (
	id          BIGSERIAL PRIMARY KEY,
	kind        TEXT NOT NULL,
	name        TEXT,
	address     TEXT,
	phone       TEXT,
	latitude    DOUBLE PRECISION,
	longitude   DOUBLE PRECISION,
	geom        geometry(Point, 4326),
	details     JSONB,
	source      TEXT NOT NULL DEFAULT 'user',
	reported_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_resources_geom ON resources USING GIST (geom);
CREATE INDEX IF NOT EXISTS idx_resources_kind ON resources (kind);
