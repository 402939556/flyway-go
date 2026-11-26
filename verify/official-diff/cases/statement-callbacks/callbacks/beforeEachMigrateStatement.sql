INSERT INTO stmt_events(seq, event) SELECT COALESCE(MAX(seq), 0) + 1, 'before-statement' FROM stmt_events;
