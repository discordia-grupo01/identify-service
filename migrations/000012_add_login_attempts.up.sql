-- Fuerza bruta contra login: en vez de bloquear la cuenta por email (eso
-- deja que cualquiera que sepa tu correo te saque de tu propia cuenta a
-- proposito, sin ni siquiera saber tu contrasena), se cuenta por la
-- combinacion (email, ip). Un atacante que no comparte tu red no te puede
-- bloquear a vos; solo se frena a si mismo.
CREATE TABLE login_attempts (
    id         BIGSERIAL PRIMARY KEY,
    email      VARCHAR(255) NOT NULL,
    ip         VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_login_attempts_email_ip_created_at
    ON login_attempts (email, ip, created_at);
