CREATE TYPE mood AS ENUM ('happy', 'sad');
CREATE DOMAIN positive_int AS INT CHECK (VALUE > 0);
CREATE TYPE pair_type AS (a INT, b TEXT);

CREATE TABLE t_complex (id INT, m mood, d positive_int);
CREATE SEQUENCE seq_complex;
CREATE VIEW v_complex AS SELECT id FROM t_complex;
CREATE MATERIALIZED VIEW mv_complex AS SELECT id FROM t_complex;

CREATE FUNCTION overloaded(x INT) RETURNS INT LANGUAGE sql AS $$ SELECT x $$;
CREATE FUNCTION overloaded(x TEXT) RETURNS TEXT LANGUAGE sql AS $$ SELECT x $$;

CREATE PROCEDURE proc_complex(x INT) LANGUAGE plpgsql AS $$
BEGIN
    NULL;
END
$$;

CREATE FUNCTION trg_complex_fn() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RETURN NEW;
END
$$;

CREATE TABLE trg_complex_table (id INT);
CREATE TRIGGER trg_complex
BEFORE INSERT ON trg_complex_table
FOR EACH ROW EXECUTE FUNCTION trg_complex_fn();

CREATE AGGREGATE sum_complex(INT) (
    SFUNC = int4pl,
    STYPE = INT
);