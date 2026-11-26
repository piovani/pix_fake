-- The seed keys get the RequestIds the DICT mock seed creates them with: a CID is keyed by the RequestId, so the
-- reconciliation needs both sides to agree on it. Only the seed's own rows change.
UPDATE pix_keys SET request_id = '462dd5b8-4828-40bf-b224-359a271bd7c9' WHERE key = '52998224725' AND request_id = '0a1b2c3d-1000-4000-8000-000000000001';
UPDATE pix_keys SET request_id = 'b7d16d8f-f1a5-4106-b507-f735142919d9' WHERE key = '+5511999990001' AND request_id = '0a1b2c3d-1000-4000-8000-000000000002';
UPDATE pix_keys SET request_id = 'f432103e-5079-4cdf-877c-982a8efeaecb' WHERE key = 'ana.souza@example.com' AND request_id = '0a1b2c3d-1000-4000-8000-000000000003';
UPDATE pix_keys SET request_id = '8f47aee1-70d1-4be6-9315-d17c2ac4ebc0' WHERE key = '8f3c1a52-6b7d-4e0a-9c11-2d5a7b3e9f01' AND request_id = '0a1b2c3d-1000-4000-8000-000000000004';
UPDATE pix_keys SET request_id = 'd3b39643-440e-4ae0-900b-9f09f274c815' WHERE key = '11144477735' AND request_id = '0a1b2c3d-1000-4000-8000-000000000005';
UPDATE pix_keys SET request_id = '75bb4f7a-e4be-42ec-9841-4634c04d049e' WHERE key = '+5511999990002' AND request_id = '0a1b2c3d-1000-4000-8000-000000000006';
UPDATE pix_keys SET request_id = '34197520-2f30-484f-b66b-bd80dee58ecb' WHERE key = '11222333000181' AND request_id = '0a1b2c3d-1000-4000-8000-000000000007';
UPDATE pix_keys SET request_id = '5f49fb05-03c8-4bc1-9e74-0e82d69d367f' WHERE key = 'contato@paoquente.example.com' AND request_id = '0a1b2c3d-1000-4000-8000-000000000008';
