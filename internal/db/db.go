// Package db เชื่อมต่อ Postgres และสร้าง schema
package db

import (
	"database/sql"

	_ "github.com/lib/pq" // driver postgres
)

// Connect เปิด connection ไป Postgres (driver lib/pq) + ping
func Connect(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	// จำกัด pool พอสำหรับเดโม กัน connection รุ่นใหญ่
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Migrate สร้างตารางถ้ายังไม่มี (idempotent) พร้อม index
func Migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id            SERIAL PRIMARY KEY,
			username      TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role          TEXT NOT NULL DEFAULT 'viewer',
			created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS customers (
			id         SERIAL PRIMARY KEY,
			name       TEXT NOT NULL,
			company    TEXT NOT NULL DEFAULT '',
			contact    TEXT NOT NULL DEFAULT '',
			email      TEXT NOT NULL DEFAULT '',
			phone      TEXT NOT NULL DEFAULT '',
			address    TEXT NOT NULL DEFAULT '',
			tax_id     TEXT NOT NULL DEFAULT '',
			note       TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS services (
			id              SERIAL PRIMARY KEY,
			customer_id     INTEGER NOT NULL REFERENCES customers ON DELETE CASCADE,
			program_name    TEXT NOT NULL DEFAULT '',
			domain          TEXT NOT NULL DEFAULT '',
			username        TEXT NOT NULL DEFAULT '',
			password_enc    TEXT NOT NULL DEFAULT '',
			registered_from DATE,
			registered_to   DATE,
			ref_invoice     TEXT NOT NULL DEFAULT '',
			ref_quotation   TEXT NOT NULL DEFAULT '',
			price           NUMERIC(12,2) NOT NULL DEFAULT 0,
			amount_paid     NUMERIC(12,2) NOT NULL DEFAULT 0,
			paid_date       DATE,
			quote_status    TEXT NOT NULL DEFAULT 'none',
			quote_date      DATE,
			renew_status    TEXT NOT NULL DEFAULT 'none',
			notes           TEXT NOT NULL DEFAULT '',
			created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS notifications (
			id         SERIAL PRIMARY KEY,
			service_id INTEGER NOT NULL REFERENCES services ON DELETE CASCADE,
			email_to   TEXT NOT NULL DEFAULT '',
			type       TEXT NOT NULL DEFAULT '',
			status     TEXT NOT NULL DEFAULT '',
			detail     TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS audit_log (
			id         SERIAL PRIMARY KEY,
			username   TEXT NOT NULL DEFAULT '',
			action     TEXT NOT NULL DEFAULT '',
			target     TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		// index เพื่อรองรับข้อมูลขนาดใหญ่
		`CREATE INDEX IF NOT EXISTS idx_services_registered_to ON services(registered_to)`,
		`CREATE INDEX IF NOT EXISTS idx_services_customer_id ON services(customer_id)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_service_type ON notifications(service_id, type, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_created_at ON notifications(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_log_created_at ON audit_log(created_at)`,
		// migration สำหรับตารางที่สร้างไว้ก่อนเพิ่มคอลัมน์
		`ALTER TABLE customers ADD COLUMN IF NOT EXISTS company TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE customers ADD COLUMN IF NOT EXISTS contact TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE customers ADD COLUMN IF NOT EXISTS address TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE customers ADD COLUMN IF NOT EXISTS tax_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE services ADD COLUMN IF NOT EXISTS ref_invoice TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE services ADD COLUMN IF NOT EXISTS ref_quotation TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE services ADD COLUMN IF NOT EXISTS price NUMERIC(12,2) NOT NULL DEFAULT 0`,
		`ALTER TABLE services ADD COLUMN IF NOT EXISTS amount_paid NUMERIC(12,2) NOT NULL DEFAULT 0`,
		`ALTER TABLE services ADD COLUMN IF NOT EXISTS paid_date DATE`,
		`ALTER TABLE services ADD COLUMN IF NOT EXISTS quote_status TEXT NOT NULL DEFAULT 'none'`,
		`ALTER TABLE services ADD COLUMN IF NOT EXISTS quote_date DATE`,
		`ALTER TABLE services ADD COLUMN IF NOT EXISTS renew_status TEXT NOT NULL DEFAULT 'none'`,
		`ALTER TABLE services ADD COLUMN IF NOT EXISTS notes TEXT NOT NULL DEFAULT ''`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}
