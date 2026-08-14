-- Los ofrecimientos pueden cubrir una ciudad completa sin coordenadas ficticias.
ALTER TABLE resources ADD COLUMN IF NOT EXISTS location_scope TEXT NOT NULL DEFAULT 'point';
ALTER TABLE resources ADD COLUMN IF NOT EXISTS municipality TEXT NOT NULL DEFAULT '';
ALTER TABLE resources ADD COLUMN IF NOT EXISTS department TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_resources_location_scope ON resources (location_scope);
CREATE INDEX IF NOT EXISTS idx_resources_municipality ON resources (municipality);
