CREATE TABLE a(id int);
CREATE INDEX CONCURRENTLY idx_a ON a(id);
SELECT $$hello;world$$;
SELECT $tag$hello;world$tag$;
/* outer /* nested */ still comment */
SELECT 1;