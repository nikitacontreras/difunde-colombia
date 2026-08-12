-- Migración 0006: Limpiar tabla de recursos e insertar las 22 ollas comunitarias reales
TRUNCATE TABLE resources RESTART IDENTITY;

INSERT INTO resources (kind, name, address, phone, latitude, longitude, geom, details, status)
VALUES 
(
  'olla_comunitaria', 
  'Semillas de Mujeres Emprendedoras', 
  'Calle 83a # 28d1 - 43 / Mojica 1, Cali', 
  '', 
  3.411, 
  -76.478, 
  ST_SetSRID(ST_MakePoint(-76.478, 3.411), 4326),
  '{"urgency": "normal", "needs": ["Arroz", "Aceite", "Agua", "Verduras", "Carnes"], "helping": 2, "needed": 1, "confirms": 10, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Olla Comunitaria - Heladería Ospy', 
  'CRA 32a # 35G1- 32, Cali', 
  '3185678996', 
  3.426, 
  -76.497, 
  ST_SetSRID(ST_MakePoint(-76.497, 3.426), 4326),
  '{"urgency": "urgente", "needs": ["Arroz", "Aceite", "Agua embotellada", "Verdura", "Pollo", "Carne"], "helping": 3, "needed": 2, "confirms": 12, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Comedor Oromo café librería', 
  'Cl. 17 #85-27, Comuna 17, Cali', 
  '', 
  3.376, 
  -76.536, 
  ST_SetSRID(ST_MakePoint(-76.536, 3.376), 4326),
  '{"urgency": "normal", "needs": ["Comida para cocinar", "Papa"], "helping": 1, "needed": 1, "confirms": 5, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Olla Restaurante Caba Restó', 
  'Calle 2 #4-52, Cali', 
  '3222843984', 
  3.447, 
  -76.539, 
  ST_SetSRID(ST_MakePoint(-76.539, 3.447), 4326),
  '{"urgency": "urgente", "needs": ["Verduras", "Proteína animal", "Empaques", "Cucharas", "Bebidas embotelladas"], "helping": 4, "needed": 1, "confirms": 15, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Olla Corregimiento El Hormiguero', 
  'Corregimiento El Hormiguero, Cali', 
  '3218214485', 
  3.315, 
  -76.490, 
  ST_SetSRID(ST_MakePoint(-76.490, 3.315), 4326),
  '{"urgency": "normal", "needs": ["Verduras", "Carne", "Agua", "Frijoles"], "helping": 2, "needed": 2, "confirms": 8, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Olla Parque antiguo Brasa Roja', 
  'Calle 27 entre cra.35 y 31, Cali', 
  '3043916753', 
  3.425, 
  -76.515, 
  ST_SetSRID(ST_MakePoint(-76.515, 3.425), 4326),
  '{"urgency": "urgente", "needs": ["Alimentos", "Verdura", "Pipa de gas"], "helping": 3, "needed": 3, "confirms": 21, "dismisses": 1}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Olla Restaurante La Porteña', 
  'Calle 3 # 6-21, San Antonio, Cali', 
  '3186968590', 
  3.448, 
  -76.541, 
  ST_SetSRID(ST_MakePoint(-76.541, 3.448), 4326),
  '{"urgency": "normal", "needs": ["Pausa en las ayudas (no hay abasto)"], "helping": 5, "needed": 0, "confirms": 30, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Comedor Puerto Resistencia', 
  'Cra 47b #41-34, Monumento Resistencia, Cali', 
  '3045666320', 
  3.418, 
  -76.502, 
  ST_SetSRID(ST_MakePoint(-76.502, 3.418), 4326),
  '{"urgency": "urgente", "needs": ["Alimentos para cocinar", "Colchonetas", "Carpas"], "helping": 8, "needed": 5, "confirms": 42, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Olla Carrera 46 Santa Cecilia', 
  'Calle 55b #441114, Cali', 
  '', 
  3.412, 
  -76.516, 
  ST_SetSRID(ST_MakePoint(-76.516, 3.412), 4326),
  '{"urgency": "normal", "needs": ["Todo lo necesario para alimentación"], "helping": 2, "needed": 2, "confirms": 11, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Olla Caseta San Francisco', 
  'CRA 34 Cl 2 Sur barrio San Francisco, Buenaventura', 
  '3025971093', 
  3.878, 
  -77.031, 
  ST_SetSRID(ST_MakePoint(-77.031, 3.878), 4326),
  '{"urgency": "urgente", "needs": ["Alimentos básicos", "Arroz", "Granos", "Carnes", "Verduras", "Aceite", "Condimentos", "Platos", "Servilletas"], "helping": 4, "needed": 3, "confirms": 16, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Olla Loma de la Cruz', 
  'Calle 2A #14 - 45, Cali', 
  '3006400501', 
  3.442, 
  -76.540, 
  ST_SetSRID(ST_MakePoint(-76.540, 3.442), 4326),
  '{"urgency": "urgente", "needs": ["Verduras", "Granos", "Desechables"], "helping": 3, "needed": 1, "confirms": 19, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Caseta Comunal San Francisco', 
  'CRA 34 calle 2 sur barrio San Francisco, Buenaventura', 
  '3135321127', 
  3.878, 
  -77.031, 
  ST_SetSRID(ST_MakePoint(-77.031, 3.878), 4326),
  '{"urgency": "urgente", "needs": ["Alimentos básicos", "Arroz", "Granos", "Carnes", "Verduras", "Aceite", "Condimentos", "Botellones de agua", "Elementos de aseo"], "helping": 5, "needed": 2, "confirms": 14, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Olla Barrio María Eugenia', 
  'Calle 6 41c-22 barrio María Eugenia, Buenaventura', 
  '3153486972', 
  3.882, 
  -77.026, 
  ST_SetSRID(ST_MakePoint(-77.026, 3.882), 4326),
  '{"urgency": "normal", "needs": ["Alimentos no perecederos"], "helping": 2, "needed": 2, "confirms": 7, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Acopio Univalle Buenaventura', 
  'Av Simón Bolívar frente a la Univalle, Buenaventura', 
  '3117099376', 
  3.874, 
  -76.990, 
  ST_SetSRID(ST_MakePoint(-76.990, 3.874), 4326),
  '{"urgency": "normal", "needs": ["Arroz", "Aceite", "Leche en polvo", "Pastas", "Lentejas", "Frijoles", "Panelas", "Azúcar"], "helping": 3, "needed": 1, "confirms": 13, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Olla San Cayetano', 
  'San Cayetano, Cali', 
  '3177468127', 
  3.441, 
  -76.544, 
  ST_SetSRID(ST_MakePoint(-76.544, 3.441), 4326),
  '{"urgency": "normal", "needs": ["Víveres", "Granos"], "helping": 1, "needed": 1, "confirms": 6, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Olla Paola CRA 9', 
  'CRA 9 # 4 -25, Cali', 
  '', 
  3.447, 
  -76.541, 
  ST_SetSRID(ST_MakePoint(-76.541, 3.447), 4326),
  '{"urgency": "urgente", "needs": ["Víveres", "Comida preparada"], "helping": 2, "needed": 2, "confirms": 9, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Olla Estación MIO Guadalupe', 
  'Calle 5ta con carrera 56, Cali', 
  '3178354850', 
  3.411, 
  -76.539, 
  ST_SetSRID(ST_MakePoint(-76.539, 3.411), 4326),
  '{"urgency": "normal", "needs": ["Insumos de cocina", "Comida", "Carpas", "Transporte de distribución"], "helping": 4, "needed": 1, "confirms": 15, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Olla Torres del Limonar', 
  'Capri, Calle 10bis # 70, Cali', 
  '3016560197', 
  3.398, 
  -76.536, 
  ST_SetSRID(ST_MakePoint(-76.536, 3.398), 4326),
  '{"urgency": "normal", "needs": ["Alimentos"], "helping": 2, "needed": 1, "confirms": 8, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Olla Reservas de Meléndez', 
  'CRA 94C #2-46, Cali', 
  '3186533446', 
  3.366, 
  -76.548, 
  ST_SetSRID(ST_MakePoint(-76.548, 3.366), 4326),
  '{"urgency": "urgente", "needs": ["Alimentos", "Cobijas", "Almohadas", "Productos de aseo personal"], "helping": 5, "needed": 3, "confirms": 22, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Olla Henry Fernández', 
  'Calle 9B # 41-11, Cali', 
  '', 
  3.424, 
  -76.538, 
  ST_SetSRID(ST_MakePoint(-76.538, 3.424), 4326),
  '{"urgency": "normal", "needs": ["Repartidores", "Comida para 100 personas"], "helping": 3, "needed": 1, "confirms": 11, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Olla Llano Verde', 
  'Calle 56i 47A 18, Cali', 
  '3117745703', 
  3.399, 
  -76.495, 
  ST_SetSRID(ST_MakePoint(-76.495, 3.399), 4326),
  '{"urgency": "urgente", "needs": ["Víveres"], "helping": 4, "needed": 2, "confirms": 17, "dismisses": 0}'::jsonb,
  'approved'
),
(
  'olla_comunitaria', 
  'Olla Colgate Palmolive', 
  'Calle 45 1E 66, Cali', 
  '3024172975', 
  3.435, 
  -76.505, 
  ST_SetSRID(ST_MakePoint(-76.505, 3.435), 4326),
  '{"urgency": "normal", "needs": ["Víveres"], "helping": 2, "needed": 1, "confirms": 7, "dismisses": 0}'::jsonb,
  'approved'
);
