package main

import (
	"database/sql"
	"fmt"
	_ "modernc.org/sqlite"
)

type LogEntry struct {
	ID        int64
	SessionID int
	Timestamp string
	Level     string
	Tag       string
	PID       int
	TID       int
	Message   string
}

type FoldedLog struct {
	StartID   int64
	EndID     int64
	StartTime string
	EndTime   string
	Level     string
	Tag       string
	PID       int
	Message   string
	Count     int
}

type DB struct {
	conn *sql.DB
}

func InitDB(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	schema := `
	PRAGMA journal_mode=WAL;
	PRAGMA synchronous=NORMAL;
	CREATE TABLE IF NOT EXISTS logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id INTEGER,
		timestamp TEXT,
		level TEXT,
		tag TEXT,
		pid INTEGER,
		tid INTEGER,
		message TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_logs_session ON logs(session_id);
	CREATE INDEX IF NOT EXISTS idx_logs_level ON logs(level);
	`
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}

	return &DB{conn: db}, nil
}

func (d *DB) InsertLog(entry LogEntry) (int64, error) {
	query := `INSERT INTO logs (session_id, timestamp, level, tag, pid, tid, message) VALUES (?, ?, ?, ?, ?, ?, ?)`
	result, err := d.conn.Exec(query, entry.SessionID, entry.Timestamp, entry.Level, entry.Tag, entry.PID, entry.TID, entry.Message)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (d *DB) AppendToLog(id int64, continuation string) error {
	_, err := d.conn.Exec(`UPDATE logs SET message = message || char(10) || ? WHERE id = ?`, continuation, id)
	return err
}

func (d *DB) GetErrorSummary(sessionID int) (string, error) {
	query := `SELECT MAX(id), tag, message, COUNT(*) as count 
	          FROM logs 
			  WHERE session_id = ? AND (level = 'E' OR level = 'F') 
			  GROUP BY tag, message 
			  ORDER BY count DESC 
			  LIMIT 20`
	rows, err := d.conn.Query(query, sessionID)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	summary := "### Error Summary (Current Session)\n"
	found := false
	for rows.Next() {
		var tag, msg string
		var id int64
		var count int
		if err := rows.Scan(&id, &tag, &msg, &count); err != nil {
			return "", err
		}
		summary += fmt.Sprintf("- [%d] **%dx** [%s] %s\n", id, count, tag, msg)
		found = true
	}
	if !found {
		return "No errors found in current session.", nil
	}
	return summary, nil
}

func (d *DB) GetTagSummary(sessionID int) (string, error) {
	query := `SELECT tag, COUNT(*) as count 
	          FROM logs 
			  WHERE session_id = ? 
			  GROUP BY tag 
			  ORDER BY count DESC 
			  LIMIT 50`
	rows, err := d.conn.Query(query, sessionID)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	summary := "### Tag Summary (Current Session)\n"
	found := false
	for rows.Next() {
		var tag string
		var count int
		if err := rows.Scan(&tag, &count); err != nil {
			return "", err
		}
		summary += fmt.Sprintf("- **%d** %s\n", count, tag)
		found = true
	}
	if !found {
		return "No logs found in current session.", nil
	}
	return summary, nil
}

func (d *DB) QueryLogs(sessionID int, level string, tag string, limit int) ([]LogEntry, error) {
	query := `SELECT id, timestamp, level, tag, pid, tid, message FROM logs WHERE session_id = ?`
	args := []interface{}{sessionID}

	if level != "" {
		query += " AND level = ?"
		args = append(args, level)
	}
	if tag != "" {
		query += " AND tag LIKE ?"
		args = append(args, "%"+tag+"%")
	}

	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := d.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LogEntry
	for rows.Next() {
		var e LogEntry
		e.SessionID = sessionID
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.Level, &e.Tag, &e.PID, &e.TID, &e.Message); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func FoldLogEntries(entries []LogEntry, limit int) []FoldedLog {
	var folded []FoldedLog
	for _, e := range entries {
		if len(folded) > 0 {
			last := &folded[len(folded)-1]
			// Check if we can fold with the last entry
			if last.Level == e.Level && last.Tag == e.Tag && last.Message == e.Message && last.PID == e.PID {
				last.StartID = e.ID // entries are usually DESC, so update StartID to the older ID
				last.StartTime = e.Timestamp
				last.Count++
				continue
			}
		}

		if len(folded) >= limit {
			break
		}

		folded = append(folded, FoldedLog{
			StartID:   e.ID,
			EndID:     e.ID,
			StartTime: e.Timestamp,
			EndTime:   e.Timestamp,
			Level:     e.Level,
			Tag:       e.Tag,
			PID:       e.PID,
			Message:   e.Message,
			Count:     1,
		})
	}
	return folded
}

func (d *DB) QueryFoldedLogs(sessionID int, level string, tag string, limit int) ([]FoldedLog, error) {
	// Fetch a larger chunk to allow folding
	rawLimit := limit * 20
	if rawLimit > 1000 {
		rawLimit = 1000
	}

	query := `SELECT id, timestamp, level, tag, pid, message FROM logs WHERE session_id = ?`
	args := []interface{}{sessionID}

	if level != "" {
		query += " AND level = ?"
		args = append(args, level)
	}
	if tag != "" {
		query += " AND tag LIKE ?"
		args = append(args, "%"+tag+"%")
	}

	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, rawLimit)

	rows, err := d.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LogEntry
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.Level, &e.Tag, &e.PID, &e.Message); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}

	return FoldLogEntries(entries, limit), nil
}

func (d *DB) ClearLogs() error {
	_, err := d.conn.Exec("DELETE FROM logs")
	return err
}

func (d *DB) GetLogContext(logID int64, contextLines int) ([]LogEntry, error) {
	query := `SELECT id, timestamp, level, tag, pid, tid, message 
	          FROM logs 
			  WHERE id >= ? AND id <= ? 
			  ORDER BY id ASC`
	
	startID := logID - int64(contextLines)
	if startID < 1 {
		startID = 1
	}
	endID := logID + int64(contextLines)

	rows, err := d.conn.Query(query, startID, endID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LogEntry
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.Level, &e.Tag, &e.PID, &e.TID, &e.Message); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func (d *DB) GetLogCount(sessionID int) (int, error) {
	var count int
	err := d.conn.QueryRow("SELECT COUNT(*) FROM logs WHERE session_id = ?", sessionID).Scan(&count)
	return count, err
}

func (d *DB) SearchLogs(query string, limit int) ([]LogEntry, error) {
	sql := `SELECT id, timestamp, level, tag, pid, tid, message 
	        FROM logs 
			WHERE message LIKE ? 
			ORDER BY id DESC 
			LIMIT ?`
	rows, err := d.conn.Query(sql, "%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LogEntry
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.Level, &e.Tag, &e.PID, &e.TID, &e.Message); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func (d *DB) SearchFoldedLogs(query string, limit int) ([]FoldedLog, error) {
	rawLimit := limit * 20
	if rawLimit > 1000 {
		rawLimit = 1000
	}

	sql := `SELECT id, timestamp, level, tag, pid, message 
	        FROM logs 
			WHERE message LIKE ? 
			ORDER BY id DESC 
			LIMIT ?`
	rows, err := d.conn.Query(sql, "%"+query+"%", rawLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LogEntry
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.Level, &e.Tag, &e.PID, &e.Message); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}

	return FoldLogEntries(entries, limit), nil
}
