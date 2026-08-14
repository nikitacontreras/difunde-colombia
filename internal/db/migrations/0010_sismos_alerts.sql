-- Sismos detectados por polling del catálogo del SGC (apicatalogador).
-- Se usa para deduplicar y para saber cuáles son nuevos a la hora de
-- notificar.
CREATE TABLE IF NOT EXISTS sismo_events (
    id TEXT PRIMARY KEY,
    mag DOUBLE PRECISION,
    mag_type TEXT,
    depth DOUBLE PRECISION,
    lat DOUBLE PRECISION,
    lon DOUBLE PRECISION,
    place TEXT,
    local_time TEXT,
    utc_time TEXT,
    event_type TEXT,
    status TEXT,
    detected_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_sismo_events_detected ON sismo_events (detected_at DESC);

-- Suscripciones a notificaciones Web Push.
CREATE TABLE IF NOT EXISTS push_subscriptions (
    endpoint TEXT PRIMARY KEY,
    p256dh TEXT NOT NULL,
    auth TEXT NOT NULL,
    device TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error TEXT,
    last_error_at TIMESTAMPTZ
);

-- Settings simples clave/valor (p. ej. las claves VAPID).
CREATE TABLE IF NOT EXISTS app_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
