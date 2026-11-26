CREATE TABLE cb_copy_data (id INTEGER PRIMARY KEY, value TEXT);
COPY cb_copy_data (id, value) FROM STDIN;
1	hello
2	${copy_word}
\.
