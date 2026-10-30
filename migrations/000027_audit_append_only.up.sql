-- migrations/000027_audit_append_only.up.sql
-- audit_log es append-only también en la base: UPDATE, DELETE y TRUNCATE se
-- rechazan con un trigger (antes solo era una convención del código).
--
-- Checkpoints de la cadena: un job del worker guarda periódicamente la cabeza
-- de la cadena (id + hash) y la loguea (ancla fuera de la base). VerifyChain
-- exige que cada checkpoint siga presente en audit_log con el mismo hash: así
-- se detecta el truncamiento de la cola o la reescritura de la cadena.
--
-- Los triggers no frenan a un superusuario que los deshabilite
-- (ALTER TABLE … DISABLE TRIGGER, session_replication_role): el rol de la
-- aplicación no debería ser dueño de la tabla en producción.

CREATE FUNCTION audit_reject_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% es append-only: % no permitido', TG_TABLE_NAME, TG_OP
        USING ERRCODE = 'insufficient_privilege';
END;
$$;

CREATE TRIGGER audit_log_append_only
    BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_reject_mutation();

CREATE TRIGGER audit_log_no_truncate
    BEFORE TRUNCATE ON audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION audit_reject_mutation();

CREATE TABLE audit_chain_checkpoints (
    id          BIGSERIAL   PRIMARY KEY,
    entry_id    BIGINT      NOT NULL,   -- audit_log.id de la cabeza al momento del checkpoint
    entry_hash  BYTEA       NOT NULL,   -- audit_log.hash de esa entrada
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER audit_chain_checkpoints_append_only
    BEFORE UPDATE OR DELETE ON audit_chain_checkpoints
    FOR EACH ROW EXECUTE FUNCTION audit_reject_mutation();

CREATE TRIGGER audit_chain_checkpoints_no_truncate
    BEFORE TRUNCATE ON audit_chain_checkpoints
    FOR EACH STATEMENT EXECUTE FUNCTION audit_reject_mutation();

COMMENT ON TABLE audit_chain_checkpoints IS
    'Cabezas de la cadena de audit_log guardadas periódicamente (append-only). VerifyChain exige que sigan presentes con el mismo hash.';
