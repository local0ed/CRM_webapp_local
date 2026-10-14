package agents

import (
	"context"
	"log"
	"time"

	"crm/internal/mail"
	"crm/internal/notify"
	"crm/internal/repo"
)

// NotifyAgent รันตรวจ service ใกล้หมดอายุและส่งอีเมลแจ้งเตือนเป็นรอบ
// (รอบแรกทำงานทันทีที่เริ่ม แล้วทำซ้ำทุก interval)
type NotifyAgent struct {
	store    *repo.Store
	mailer   *mail.Mailer
	logger   *log.Logger
	interval time.Duration // ปกติ 24 ชั่วโมง
}

// NewNotify สร้างเอเจนต์แจ้งเตือน
func NewNotify(store *repo.Store, mailer *mail.Mailer, logger *log.Logger, interval time.Duration) *NotifyAgent {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	return &NotifyAgent{store: store, mailer: mailer, logger: logger, interval: interval}
}

func (a *NotifyAgent) Name() string { return "notify" }

// Run ตรวจและแจ้งเตือนทุก interval; ถ้ารอบใดผิดพลาด (เช่น DB ล่มชั่วคราว)
// จะ log แล้วลองใหม่ใน 1 นาที ไม่ต้องรีสตาร์ททั้งตัว
func (a *NotifyAgent) Run(ctx context.Context) error {
	delay := a.interval
	for {
		n, err := notify.CheckAndNotify(a.store.DB, a.mailer)
		if err != nil {
			a.logger.Printf("[notify] ผิดพลาด: %v (สร้างไปแล้ว %d ฉบับ) — ลองใหม่ใน 1 นาที", err, n)
			delay = time.Minute
		} else {
			a.logger.Printf("[notify] ตรวจแล้ว สร้างการแจ้งเตือน %d ฉบับ — รอบถัดไปในอีก %s", n, delay)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
	}
}

// Health ตรวจว่า DB ยังตอบสนอง (งานหลักของเอเจนต์นี้คือ query ฐานข้อมูล)
func (a *NotifyAgent) Health(ctx context.Context) error {
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return a.store.DB.PingContext(pctx)
}
