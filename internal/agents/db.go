// Package agents เอเจนต์ที่ใช้งานจริงของระบบ CRM แต่ละตัวรับผิดชอบงานส่วนเดียว
package agents

import (
	"context"
	"database/sql"
	"log"
	"time"
)

// DBAgent ดูแลการเชื่อมต่อฐานข้อมูล: ping เป็นระยะ เรียก onReady ครั้งแรกที่พร้อม
// (migrate schema, seed ข้อมูล) ถ้า ping ล้มเหลวจะคืน error ให้ manager รีสตาร์ท
// — connection pool ของ database/sql ต่อใหม่เองอัตโนมัติเมื่อ Postgres กลับมา
type DBAgent struct {
	db      *sql.DB
	logger  *log.Logger
	onReady func(context.Context, *sql.DB) error // migrate + seed (เรียกครั้งเดียว)
	ready   bool
}

// NewDB สร้างเอเจนต์ฐานข้อมูล; onReady อาจเป็น nil
func NewDB(db *sql.DB, logger *log.Logger, onReady func(context.Context, *sql.DB) error) *DBAgent {
	return &DBAgent{db: db, logger: logger, onReady: onReady}
}

func (a *DBAgent) Name() string { return "db" }

// Run รอ DB พร้อม → migrate/seed ครั้งเดียว → ping เป็นระยะเพื่อเฝ้าดู
func (a *DBAgent) Run(ctx context.Context) error {
	// รอจนเชื่อมต่อได้ (ระบบรอ DB ได้นานเท่าที่ต้องการ ไม่ต้องตายตั้งแต่ตอนเปิด)
	for {
		if err := a.db.PingContext(ctx); err == nil {
			a.logger.Printf("[db] เชื่อมต่อฐานข้อมูลสำเร็จ")
			break
		} else {
			a.logger.Printf("[db] ยังเชื่อมต่อฐานข้อมูลไม่ได้ (%v) — ลองใหม่ใน 5 วินาที", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}

	// พร้อมแล้ว → เตรียม schema/ข้อมูลเริ่มต้นครั้งเดียว
	if a.onReady != nil && !a.ready {
		if err := a.onReady(ctx, a.db); err != nil {
			return err // manager จะรีสตาร์ทให้ลองอีกครั้ง
		}
		a.ready = true
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := a.db.PingContext(ctx); err != nil {
				return err // แจ้ง manager ให้จัดการต่อ
			}
		}
	}
}

// Health ping ฐานข้อมูลด้วย timeout สั้น ๆ
func (a *DBAgent) Health(ctx context.Context) error {
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return a.db.PingContext(pctx)
}
