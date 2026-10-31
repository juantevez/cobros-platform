-- migrations/000046_payment_drop_unused_columns.down.sql
ALTER TABLE payments
    ADD COLUMN checkout_id   TEXT,
    ADD COLUMN authorized_at TIMESTAMPTZ;
