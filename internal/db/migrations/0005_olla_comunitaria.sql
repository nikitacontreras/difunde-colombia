-- Migración 0005: Insertar ollas comunitarias de prueba realistas
INSERT INTO resources (kind, name, address, phone, latitude, longitude, geom, details, status)
VALUES 
(
  'olla_comunitaria', 
  'Olla Comunitaria - Polideportivo Barrio Compartir', 
  'Carrera 25 con Calle 84, Polideportivo Compartir', 
  '3156667777', 
  3.425, 
  -76.495, 
  ST_SetSRID(ST_MakePoint(-76.495, 3.425), 4326),
  '{"urgency": "normal", "needs": ["Leña", "Arroz", "Lentejas", "Ollas grandes", "Platos desechables"], "helping": 4, "needed": 2, "confirms": 15, "dismisses": 1}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Comedor Comunitario - Parroquia San Juan Bautista', 
  'Calle 52 # 1B-20, Salón Parroquial', 
  '3008889999', 
  3.488, 
  -76.508, 
  ST_SetSRID(ST_MakePoint(-76.508, 3.488), 4326),
  '{"urgency": "urgente", "needs": ["Gas propano", "Verduras", "Carne", "Aceite"], "helping": 2, "needed": 3, "confirms": 8, "dismisses": 0}'::jsonb,
  'approved'
);
