-- Migración 0003: Añadir estado de aprobación/moderación a los recursos de centros de acopio/ayuda.
ALTER TABLE resources ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'pending';
CREATE INDEX IF NOT EXISTS idx_resources_status ON resources (status);
