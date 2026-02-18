ALTER TABLE users
    ADD COLUMN IF NOT EXISTS first_name TEXT,
    ADD COLUMN IF NOT EXISTS last_name TEXT,
    ADD COLUMN IF NOT EXISTS birth_date DATE,
    ADD COLUMN IF NOT EXISTS avatar_data_url TEXT,
    ADD COLUMN IF NOT EXISTS avatar_gradient TEXT;

CREATE INDEX IF NOT EXISTS idx_users_birth_date ON users(birth_date);
