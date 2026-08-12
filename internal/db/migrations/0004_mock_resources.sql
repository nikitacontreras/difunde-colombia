-- Migración 0004: Insertar datos de prueba realistas para la emergencia
INSERT INTO resources (kind, name, address, phone, latitude, longitude, geom, details, status)
VALUES 
(
  'centro_acopio', 
  'Parque principal de chiminangos, al frente de los asados', 
  'Parque principal de chiminangos, al frente de los asados', 
  '3001234567', 
  3.483, 
  -76.512, 
  ST_SetSRID(ST_MakePoint(-76.512, 3.483), 4326),
  '{"urgency": "urgente", "needs": ["Colchonetas", "Pañales de diferentes edades", "Pañitos húmedos", "Pañales para adulto", "Shampoo en sobres", "Acondicionador en sobres", "Crema dental"], "helping": 0, "needed": 0, "confirms": 9, "dismisses": 3}'::jsonb,
  'approved'
),
(
  'refugio', 
  'Cra. 39 #4-28', 
  'Carrera 39 # 4-28', 
  '3127654321', 
  3.475, 
  -76.525, 
  ST_SetSRID(ST_MakePoint(-76.525, 3.475), 4326),
  '{"urgency": "urgente", "needs": ["Linternas", "Palas", "Porras", "Costales", "Baldes", "Botiquín"], "helping": 0, "needed": 1, "confirms": 4, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'agua', 
  'Calle 62 #1bis', 
  'Calle 62 # 1bis', 
  '3159998888', 
  3.491, 
  -76.505, 
  ST_SetSRID(ST_MakePoint(-76.505, 3.491), 4326),
  '{"urgency": "urgente", "needs": ["Palas", "Picas", "Seguetas", "Colchonetas", "Carpas"], "helping": 0, "needed": 3, "confirms": 12, "dismisses": 1}'::jsonb,
  'approved'
);
