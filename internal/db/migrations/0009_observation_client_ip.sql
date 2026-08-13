-- Migración 0009: Añadir columna client_ip a las observaciones para auditoría y geolocalización.
ALTER TABLE observations ADD COLUMN IF NOT EXISTS client_ip TEXT;
