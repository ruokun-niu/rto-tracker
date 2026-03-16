package db

import (
	"database/sql"
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
	Active         bool   `json:"active"`
}

// WeekSummary holds badge count for a given week.
type WeekSummary struct {
	WeekStart string `json:"week_start"` // Monday date YYYY-MM-DD
	WeekEnd   string `json:"week_end"`   // Sunday date
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
			active INTEGER NOT NULL DEFAULT 0
		)`,
	}
	for _, s := range statements {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("exec %q: %w", s, err)
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
	row := s.db.QueryRow("SELECT id, name, days_per_week, policy_type, window_weeks, best_weeks, active FROM policies WHERE active = 1 LIMIT 1")
	p := &PolicyConfig{}
	var active int
	err := row.Scan(&p.ID, &p.Name, &p.DaysPerWeek, &p.PolicyType, &p.WindowWeeks, &p.BestWeeks, &active)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Active = active == 1
	return p, nil
}

// GetPolicies returns all policies.
func (s *Store) GetPolicies() ([]PolicyConfig, error) {
	rows, err := s.db.Query("SELECT id, name, days_per_week, policy_type, window_weeks, best_weeks, active FROM policies ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var policies []PolicyConfig
	for rows.Next() {
		var p PolicyConfig
		var active int
		if err := rows.Scan(&p.ID, &p.Name, &p.DaysPerWeek, &p.PolicyType, &p.WindowWeeks, &p.BestWeeks, &active); err != nil {
			return nil, err
		}
		p.Active = active == 1
		policies = append(policies, p)
	}
	return policies, rows.Err()
}

// CreatePolicy inserts a new policy.
func (s *Store) CreatePolicy(p PolicyConfig) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO policies (name, days_per_week, policy_type, window_weeks, best_weeks, active)
		VALUES (?, ?, ?, ?, ?, ?)`, p.Name, p.DaysPerWeek, p.PolicyType, p.WindowWeeks, p.BestWeeks, boolToInt(p.Active))
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

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}
