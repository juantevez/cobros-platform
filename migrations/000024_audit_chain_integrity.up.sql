-- migrations/000024_audit_chain_integrity.up.sql
-- Defensa en profundidad del hash chain de audit_log: una bifurcación (dos
-- entradas encadenadas al mismo eslabón) no puede persistirse. La aplicación
-- ya serializa los registros con pg_advisory_xact_lock (RecordAction); estos
-- índices hacen que un error de serialización falle en vez de corromper la
-- cadena en silencio.
--
-- Si la tabla ya tiene una bifurcación, esta migración falla: es corrupción
-- existente que hay que investigar antes de continuar.

CREATE UNIQUE INDEX uq_audit_log_prev_hash ON audit_log (prev_hash)
    WHERE prev_hash IS NOT NULL;

-- Una sola entrada génesis (prev_hash NULL).
CREATE UNIQUE INDEX uq_audit_log_genesis ON audit_log ((prev_hash IS NULL))
    WHERE prev_hash IS NULL;
