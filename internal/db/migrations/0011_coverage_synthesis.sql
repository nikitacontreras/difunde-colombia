-- Síntesis de cobertura derivada de los mapas públicos de los
-- operadores (Movistar, Claro, Tigo, WOM). NO es el baseline oficial
-- de la CRC: es una conversión de los mapas raster de cada operador a
-- datos consultables (municipios y celdas H3 res 7, ~1.2 km de borde).
-- Se regenera con el pipeline synthesize_coverage.py; la tabla es
-- volátil y se reemplaza completa en cada carga.

CREATE TABLE IF NOT EXISTS coverage_synthesis (
	dane_code     TEXT NOT NULL,
	department    TEXT NOT NULL DEFAULT '',
	municipality  TEXT NOT NULL,
	operator      TEXT NOT NULL,
	technology    TEXT NOT NULL,
	covered_ratio DOUBLE PRECISION NOT NULL,
	covered_km2   DOUBLE PRECISION NOT NULL DEFAULT 0,
	area_km2      DOUBLE PRECISION NOT NULL DEFAULT 0,
	PRIMARY KEY (dane_code, operator, technology)
);
CREATE INDEX IF NOT EXISTS idx_coverage_synthesis_muni ON coverage_synthesis (municipality);
CREATE INDEX IF NOT EXISTS idx_coverage_synthesis_op ON coverage_synthesis (operator);

CREATE TABLE IF NOT EXISTS coverage_synthesis_cells (
	h3         TEXT NOT NULL,
	operator   TEXT NOT NULL,
	technology TEXT NOT NULL,
	PRIMARY KEY (h3, operator, technology)
);
CREATE INDEX IF NOT EXISTS idx_coverage_synthesis_cells_op ON coverage_synthesis_cells (operator);
CREATE INDEX IF NOT EXISTS idx_coverage_synthesis_cells_h3 ON coverage_synthesis_cells (h3);

CREATE TABLE IF NOT EXISTS coverage_synthesis_meta (
	id          SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
	generated_at TIMESTAMPTZ NOT NULL,
	source      TEXT NOT NULL,
	h3_res      INTEGER NOT NULL,
	updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
