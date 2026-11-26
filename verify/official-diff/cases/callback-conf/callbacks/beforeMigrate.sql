CREATE TABLE cb_conf (id INTEGER PRIMARY KEY, value TEXT);
INSERT INTO cb_conf(id, value) VALUES (1, '${not_replaced}');
