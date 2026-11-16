// Copyright (c) 2026 Ruokun (Tommy) Niu
// Licensed under the MIT License. See LICENSE file in the project root for details.

package db

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Store manages all database operations for the RTO tracker.
type Store struct {
	db *sql.DB
}

// BadgeRecord represents a single badge-in event.
type BadgeRecord struct {
	ID   int64  `json:"id"`
	Date string `json:"date"` // YYYY-MM-DD
}

// PolicyConfig represents the company RTO policy.
type PolicyConfig struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	DaysPerWeek    int    `json:"days_per_week"`    // 3, 4, or 5
	PolicyType     string `json:"policy_type"`      // "belt_average", "belt_strict", "straight"
	WindowWeeks    int    `json:"window_weeks"`     // e.g., 12
	BestWeeks      int    `json:"best_weeks"`       // e.g., 8 (only for BELT policies)
	StartDate      string `json:"start_date"`       // Date from which RTO tracking begins (YYYY-MM-DD)
	Active         bool   `json:"active"`
}

// WeekSummary holds badge count for a given week.
type WeekSummary struct {
	WeekStart string `json:"week_start"` // Sunday date YYYY-MM-DD
	WeekEnd   string `json:"week_end"`   // Saturday date
	Count     int    `json:"count"`
}

// New opens (or creates) the SQLite database stored in the user's config directory.
func New() (*Store, error) {
	dir, err := configDir()
	if err != nil {
		return nil, fmt.Errorf("config dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}
	dbPath := filepath.Join(dir, "rto-tracker.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".rto-tracker"), nil
}

func migrate(db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS badge_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			date TEXT NOT NULL UNIQUE
		)`,
		`CREATE TABLE IF NOT EXISTS policies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			days_per_week INTEGER NOT NULL DEFAULT 3,
			policy_type TEXT NOT NULL DEFAULT 'belt_average',
			window_weeks INTEGER NOT NULL DEFAULT 12,
			best_weeks INTEGER NOT NULL DEFAULT 8,
			start_date TEXT,
			active INTEGER NOT NULL DEFAULT 0
		)`,
	}
	for _, s := range statements {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("exec %q: %w", s, err)
		}
	}

	// Add start_date column if it doesn't exist (migration for existing databases)
	var hasStartDate int
	err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('policies') WHERE name='start_date'").Scan(&hasStartDate)
	if err == nil && hasStartDate == 0 {
		_, err = db.Exec("ALTER TABLE policies ADD COLUMN start_date TEXT")
		if err != nil {
			return fmt.Errorf("add start_date column: %w", err)
		}
	}

	// Seed a default policy if none exists.
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM policies").Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		_, err := db.Exec(`INSERT INTO policies (name, days_per_week, policy_type, window_weeks, best_weeks, active)
			VALUES ('BELT Average (3 days/wk)', 3, 'belt_average', 12, 8, 1)`)
		if err != nil {
			return err
		}
	}
	return nil
}

// --- Badge operations ---

// BadgeIn records a badge for the given date (YYYY-MM-DD). Duplicate dates are silently ignored.
func (s *Store) BadgeIn(date string) error {
	_, err := s.db.Exec("INSERT OR IGNORE INTO badge_records (date) VALUES (?)", date)
	return err
}

// BadgeInToday records a badge for today.
func (s *Store) BadgeInToday() (string, error) {
	today := time.Now().Format("2006-01-02")
	return today, s.BadgeIn(today)
}

// DeleteBadge removes a badge record by date.
func (s *Store) DeleteBadge(date string) error {
	_, err := s.db.Exec("DELETE FROM badge_records WHERE date = ?", date)
	return err
}

// GetBadges returns all badge records in date order.
func (s *Store) GetBadges() ([]BadgeRecord, error) {
	rows, err := s.db.Query("SELECT id, date FROM badge_records ORDER BY date DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []BadgeRecord
	for rows.Next() {
		var r BadgeRecord
		if err := rows.Scan(&r.ID, &r.Date); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// GetBadgesInRange returns badges between startDate and endDate (inclusive, YYYY-MM-DD).
func (s *Store) GetBadgesInRange(startDate, endDate string) ([]BadgeRecord, error) {
	rows, err := s.db.Query("SELECT id, date FROM badge_records WHERE date >= ? AND date <= ? ORDER BY date", startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []BadgeRecord
	for rows.Next() {
		var r BadgeRecord
		if err := rows.Scan(&r.ID, &r.Date); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// --- Policy operations ---

// GetActivePolicy returns the currently active policy.
func (s *Store) GetActivePolicy() (*PolicyConfig, error) {
	row := s.db.QueryRow("SELECT id, name, days_per_week, policy_type, window_weeks, best_weeks, start_date, active FROM policies WHERE active = 1 LIMIT 1")
	p := &PolicyConfig{}
	var active int
	var startDate sql.NullString
	err := row.Scan(&p.ID, &p.Name, &p.DaysPerWeek, &p.PolicyType, &p.WindowWeeks, &p.BestWeeks, &startDate, &active)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if startDate.Valid {
		p.StartDate = startDate.String
	}
	p.Active = active == 1
	return p, nil
}

// GetPolicies returns all policies.
func (s *Store) GetPolicies() ([]PolicyConfig, error) {
	rows, err := s.db.Query("SELECT id, name, days_per_week, policy_type, window_weeks, best_weeks, start_date, active FROM policies ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var policies []PolicyConfig
	for rows.Next() {
		var p PolicyConfig
		var active int
		var startDate sql.NullString
		if err := rows.Scan(&p.ID, &p.Name, &p.DaysPerWeek, &p.PolicyType, &p.WindowWeeks, &p.BestWeeks, &startDate, &active); err != nil {
			return nil, err
		}
		if startDate.Valid {
			p.StartDate = startDate.String
		}
		p.Active = active == 1
		policies = append(policies, p)
	}
	return policies, rows.Err()
}

// CreatePolicy inserts a new policy.
func (s *Store) CreatePolicy(p PolicyConfig) (int64, error) {
	var startDate interface{}
	if p.StartDate != "" {
		startDate = p.StartDate
	} else {
		startDate = nil
	}
	res, err := s.db.Exec(`INSERT INTO policies (name, days_per_week, policy_type, window_weeks, best_weeks, start_date, active)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, p.Name, p.DaysPerWeek, p.PolicyType, p.WindowWeeks, p.BestWeeks, startDate, boolToInt(p.Active))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SetActivePolicy sets the given policy as active and deactivates others.
func (s *Store) SetActivePolicy(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE policies SET active = 0"); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec("UPDATE policies SET active = 1 WHERE id = ?", id); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// DeletePolicy deletes a policy by ID.
func (s *Store) DeletePolicy(id int64) error {
	_, err := s.db.Exec("DELETE FROM policies WHERE id = ?", id)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// --- Export/Import ---

// ExportData represents all exportable data
type ExportData struct {
	Version  int             `json:"version"`
	Badges   []BadgeRecord   `json:"badges"`
	Policies []PolicyConfig  `json:"policies"`
}

// Export serializes all data to a base64-encoded string.
func (s *Store) Export() (string, error) {
	badges, err := s.GetBadges()
	if err != nil {
		return "", fmt.Errorf("get badges: %w", err)
	}
	policies, err := s.GetPolicies()
	if err != nil {
		return "", fmt.Errorf("get policies: %w", err)
	}
	
	data := ExportData{
		Version:  1,
		Badges:   badges,
		Policies: policies,
	}
	
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	
	return base64.StdEncoding.EncodeToString(jsonBytes), nil
}

// Import decodes a base64 string and replaces all data.
func (s *Store) Import(encoded string) error {
	jsonBytes, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("decode base64: %w", err)
	}
	
	var data ExportData
	if err := json.Unmarshal(jsonBytes, &data); err != nil {
		return fmt.Errorf("unmarshal json: %w", err)
	}
	
	// Begin transaction
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	
	// Clear existing data
	if _, err := tx.Exec("DELETE FROM badge_records"); err != nil {
		return fmt.Errorf("clear badges: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM policies"); err != nil {
		return fmt.Errorf("clear policies: %w", err)
	}
	
	// Insert badges
	for _, b := range data.Badges {
		if _, err := tx.Exec("INSERT INTO badge_records (date) VALUES (?)", b.Date); err != nil {
			return fmt.Errorf("insert badge %s: %w", b.Date, err)
		}
	}
	
	// Insert policies
	for _, p := range data.Policies {
		if _, err := tx.Exec(
			`INSERT INTO policies (name, days_per_week, policy_type, window_weeks, best_weeks, active)
			VALUES (?, ?, ?, ?, ?, ?)`,
			p.Name, p.DaysPerWeek, p.PolicyType, p.WindowWeeks, p.BestWeeks, boolToInt(p.Active),
		); err != nil {
			return fmt.Errorf("insert policy %s: %w", p.Name, err)
		}
	}
	
	return tx.Commit()
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}
