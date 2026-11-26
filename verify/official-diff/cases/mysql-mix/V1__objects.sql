CREATE TABLE mix_a (id int);
CREATE INDEX CONCURRENTLY idx_mix_a ON mix_a(id);
SELECT 1;
