CREATE TYPE mood AS ENUM ('happy', 'sad');
CREATE TABLE t_clean (id INT, m mood);
CREATE SEQUENCE seq_clean;
CREATE VIEW v_clean AS SELECT id FROM t_clean;
CREATE FUNCTION f_clean() RETURNS INT LANGUAGE sql AS $$ SELECT 1 $$;
