INSERT INTO stmt_events(seq, event) SELECT COALESCE(MAX(seq), 0) + 1, 'statement-error' FROM stmt_events;
