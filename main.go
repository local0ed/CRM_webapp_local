// crm — ระบบจัดการลูกค้า/สัญญาบริการ รันแบบ multi-agent
//
// main ทำหน้าที่เป็น "manager": ประกอบเอเจนต์ทุกตัว สั่งรัน ตรวจสุขภาพ
// และรีสตาร์ทอัตโนมัติเมื่อเอเจนต์ตัวใดติดขัดหรือดับ เมื่อปิดระบบ (Ctrl+C)
// ทุกเอเจนต์จะหยุดอย่างสง่างาม และเปิดใหม่มาจะอ่านสมุดบันทึก (journal)
// เพื่อ "กลับมาต่องานได้เลย" แม้ครั้งก่อนระบบจะดับกลางทาง
package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"crm/internal/agents"
	"crm/internal/api"
	"crm/internal/auth"
	"crm/internal/config"
	"crm/internal/crypto"
	"crm/internal/db"
	"crm/internal/mail"
	"crm/internal/manager"
	"crm/internal/models"
	"crm/internal/repo"
)

// journalPath ไฟล์สมุดบันทึกกิจกรรม (เก็บไว้ resume งานได้หลังปิด/ดับ)
const journalPath = "data/journal.jsonl"

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmsgprefix)

	cfg, err := config.Load()
	if err != nil {
		logger.Fatalf("config: %v", err)
	}
	if err := crypto.Init(cfg.EncryptionKey); err != nil {
		logger.Fatalf("crypto: %v", err)
	}

	// เปิด connection ทิ้งไว้ก่อน (ยังไม่ ping — DB agent จะรอจนพร้อมเอง)
	dbr, err := sql.Open("postgres", cfg.DBDSN)
	if err != nil {
		logger.Fatalf("db: %v", err)
	}
	dbr.SetMaxOpenConns(20)
	dbr.SetMaxIdleConns(5)
	store := &repo.Store{DB: dbr}

	sess := auth.NewSessions(time.Duration(cfg.SessionTTLHrs) * time.Hour)
	rl := auth.NewRateLimiter(5, time.Minute)
	mailer := mail.New(cfg)

	// ---- สมุดบันทึก: อ่านงานเดิมกลับมาก่อนเริ่มระบบ ----
	journal, err := agents.NewJournal(journalPath, logger)
	if err != nil {
		logger.Fatalf("journal: %v", err)
	}
	resumeFromJournal(journal, logger)
	journal.Append("boot", "เริ่มระบบ CRM (พอร์ท "+cfg.Port+")")

	mgr := manager.New(logger, 15*time.Second)
	mgr.SetBackoff(2*time.Second, 30*time.Second)
	mgr.SetEventSink(journal.Append) // ทุกเหตุการณ์ของ manager ลงสมุดด้วย

	// bootstrap เรียกครั้งแรกที่ DB พร้อม: migrate schema + seed บัญชีเริ่มต้น
	bootstrap := func(ctx context.Context, d *sql.DB) error {
		if err := db.Migrate(d); err != nil {
			return err
		}
		return seedAdmin(ctx, d, store, logger)
	}

	// ---- ลงทะเบียนเอเจนต์ทั้งหมด ----
	mgr.Register(agents.NewDB(dbr, logger, bootstrap))
	mgr.Register(agents.NewNotify(store, mailer, logger, 24*time.Hour))
	mgr.Register(agents.NewHTTP(":"+cfg.Port, api.New(store, sess, rl, dbr, mgr, journal, logger), logger))
	mgr.Register(journal)

	// ---- รันระบบ: บล็อกรอจนกด Ctrl+C แล้วปิดอย่างสง่างาม ----
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Printf("เริ่มระบบ CRM (multi-agent) ที่พอร์ท %s — เอเจนต์ %d ตัว | กด Ctrl+C เพื่อหยุด",
		cfg.Port, 4)
	mgr.Run(ctx)

	journal.Append("shutdown", "ระบบปิดอย่างปกติ")
	journal.Close()
	logger.Println("ระบบหยุดทำงานเรียบร้อย")
}

// resumeFromJournal อ่านสมุดครั้งก่อนแล้วสรุปให้ฟังว่า "ทำถึงไหนแล้ว"
// ถ้าครั้งก่อนปิดผิดปกติ (เช่นเครื่องดับ) จะเตือนให้รู้ตัว
func resumeFromJournal(j *agents.JournalAgent, logger *log.Logger) {
	entries, err := agents.ReadJournal(journalPath, 10)
	if err != nil || len(entries) == 0 {
		logger.Println("ยังไม่มีสมุดบันทึกเดิม — เริ่มงานใหม่")
		return
	}
	last := entries[len(entries)-1]
	if last.Kind != "shutdown" {
		logger.Printf("ครั้งก่อนระบบปิดผิดปกติ (%s: %s) — กลับมาต่องานได้เลย นี่คือสิ่งที่ทำค้างไว้:",
			last.Kind, last.Detail)
	} else {
		logger.Println("กลับมาต่อจากครั้งก่อน — กิจกรรมล่าสุด:")
	}
	n := 5
	if len(entries) < n {
		n = len(entries)
	}
	for _, e := range entries[len(entries)-n:] {
		logger.Printf("  [journal] %s | %s: %s", e.Time.Format("02 Jan 15:04:05"), e.Kind, e.Detail)
	}
}

// seedAdmin สร้างบัญชี admin เริ่มต้นถ้ายังไม่มีผู้ใช้เลย (สำหรับเดโม)
func seedAdmin(ctx context.Context, d *sql.DB, store *repo.Store, logger *log.Logger) error {
	var n int
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	hash, err := auth.HashPassword("admin123")
	if err != nil {
		return err
	}
	if err := store.CreateUser(&models.User{Username: "admin", Role: "admin"}, hash); err != nil {
		return err
	}
	logger.Println("สร้างบัญชีเริ่มต้น admin / admin123 แล้ว (เปลี่ยนรหัสผ่านก่อนใช้งานจริง!)")
	return nil
}
