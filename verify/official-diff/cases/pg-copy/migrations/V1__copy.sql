CREATE TABLE copy_data (
  id INT,
  val TEXT,
  note TEXT
);

COPY copy_data (id, val, note) FROM STDIN;
1	a	\N
2	b\tc	line1\nline2
\.