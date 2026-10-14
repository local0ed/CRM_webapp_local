// Package config อ่านการตั้งค่าของแอปจาก environment variables
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config เก็บค่าตั้งค่าทั้งหมดของแอป
type Config struct {
	Port          string // พอร์ทที่เปิดบริการ เช่น 8080
	DBDSN         string // Postgres connection string
	EncryptionKey string // key 32 bytes สำหรับ AES-256-GCM
	SessionTTLHrs int    // อายุ session (ชั่วโมง)
	MailFrom      string
	MailDryRun    bool   // true = จำลองการส่ง (default), false = ส่งจริงผ่าน SMTP
	SMTPHost      string // เช่น smtp.gmail.com
	SMTPPort      int    // 587
	SMTPUser      string
	SMTPPass      string
}

// Load อ่านค่าจาก env พร้อมค่า default สำหรับเดโม
func Load() (*Config, error) {
	cfg := &Config{
		Port:          getenv("PORT", "8080"),
		DBDSN:         getenv("DB_DSN", "postgres://localhost:5432/crm_demo?sslmode=disable"),
		EncryptionKey: getenv("ENCRYPTION_KEY", ""),
		SessionTTLHrs: getenvInt("SESSION_TTL_HOURS", 12),
		MailFrom:      getenv("MAIL_FROM", "crm-alerts@example.com"),
		MailDryRun:    getenv("MAIL_DRY_RUN", "true") != "false",
		SMTPHost:      getenv("SMTP_HOST", ""),
		SMTPPort:      getenvInt("SMTP_PORT", 587),
		SMTPUser:      getenv("SMTP_USER", ""),
		SMTPPass:      getenv("SMTP_PASS", ""),
	}

	// ในเดโม ถ้าไม่ได้ตั้ง ENCRYPTION_KEY ให้ใช้ key ที่ derive จากค่าคงที่
	// (ใน production ต้องตั้ง ENCRYPTION_KEY เป็นค่าสุ่ม 32 bytes เสมอ)
	if cfg.EncryptionKey == "" {
		cfg.EncryptionKey = getenv("ENCRYPTION_KEY_DEFAULT", "demo-key-please-change-32bytes!!")
	}
	if len(cfg.EncryptionKey) != 32 {
		return nil, fmt.Errorf("ENCRYPTION_KEY ต้องยาว 32 bytes (ปัจจุบัน %d)", len(cfg.EncryptionKey))
	}
	return cfg, nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getenvInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
