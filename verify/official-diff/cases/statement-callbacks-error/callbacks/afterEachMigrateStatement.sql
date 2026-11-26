INSERT INTO stmt_events(seq, event) SELECT COALESCE(MAX(seq), 0) + 1, 'after-statement' FROM stmt_events;
