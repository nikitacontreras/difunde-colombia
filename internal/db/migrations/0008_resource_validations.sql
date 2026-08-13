-- Migración 0008: Crear tabla para registrar las validaciones de recursos con IP y User Agent
CREATE TABLE IF NOT EXISTS resource_validations (
    id BIGSERIAL PRIMARY KEY,
    resource_id BIGINT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    vote_type TEXT NOT NULL, -- 'confirm' o 'disprove'
    ip TEXT NOT NULL,
    user_agent TEXT NOT NULL,
    fingerprint TEXT NOT NULL, -- SHA-256 hash de IP + User Agent
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Índice único para evitar votos duplicados de la misma persona en el mismo recurso
CREATE UNIQUE INDEX IF NOT EXISTS idx_resource_validations_uniq ON resource_validations (resource_id, fingerprint);
