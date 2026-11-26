CREATE TABLE quote_demo (
    id INT PRIMARY KEY,
    note VARCHAR(200) NOT NULL
);
INSERT INTO quote_demo (id, note) VALUES (1, 'semi;colon');
INSERT INTO quote_demo (id, note) VALUES (2, 'it''s fine');
-- a comment that contains a semicolon ;
/*
  block comment ; still not a statement
*/
INSERT INTO quote_demo (id, note) VALUES (3, 'done');