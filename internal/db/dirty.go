package db

func (w *Writer) markSessionDirty(id int64) error {
	if w.dirtySessions[id] {
		return nil
	}
	if _, err := w.tx.ExecContext(w.ctx, `INSERT OR IGNORE INTO dirty_sessions(session_id) VALUES (?)`, id); err != nil {
		return err
	}
	if w.dirtySessions == nil {
		w.dirtySessions = make(map[int64]bool)
	}
	w.dirtySessions[id] = true
	return nil
}

func (w *Writer) markMessageDirty(id int64) error {
	var sessionID int64
	if err := w.tx.QueryRowContext(w.ctx, `SELECT session_id FROM messages WHERE id=?`, id).Scan(&sessionID); err != nil {
		return err
	}
	return w.markSessionDirty(sessionID)
}
