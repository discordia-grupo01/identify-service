-- Usuario de prueba para los tests de contrato (base efimera y local).
-- El token de la corrida lleva este id como sub; el usuario existe, tiene el
-- email verificado y no esta suspendido. Sin password: no se usa para login.
INSERT INTO users (id, name, email, password_hash, email_verified, verified_at)
VALUES ('00000000-0000-4000-8000-0000000000b1', 'Contract User', 'contract-user@example.test', NULL, TRUE, now())
ON CONFLICT (id) DO NOTHING;
