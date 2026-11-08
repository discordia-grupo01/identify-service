-- The throttler evaluates the combined (email, ip), email-only and ip-only
-- buckets. These indexes keep the wider shared-network buckets inexpensive.
CREATE INDEX idx_login_attempts_email_created_at
    ON login_attempts (email, created_at);

CREATE INDEX idx_login_attempts_ip_created_at
    ON login_attempts (ip, created_at);
