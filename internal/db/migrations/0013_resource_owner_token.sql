-- La credencial de edición se genera en el navegador; solo se persiste su hash.
ALTER TABLE resources ADD COLUMN IF NOT EXISTS owner_token_hash TEXT NOT NULL DEFAULT '';
