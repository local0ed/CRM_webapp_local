package manager

import (
	"context"
	"errors"
	"io"
	"log"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAgent เอเจนต์จำลอง: Health ล้มตามจำนวนครั้งที่ตั้ง แล้วปกติ
type fakeAgent struct {
	name       string
	failHealth atomic.Int32 // จำนวนครั้งแรกที่ Health ควรคืน error (นับถอยหลัง)
	runs       atomic.Int32 // จำนวนครั้งที่ Run ถูกเรียก (= 1 + จำนวนรีสตาร์ท)
}

func (f *fakeAgent) Name() string { return f.name }

func (f *fakeAgent) Run(ctx context.Context) error {
	f.runs.Add(1)
	<-ctx.Done() // บล็อกจนกว่าจะถูกยกเลิก เหมือนเอเจนต์จริง
	return nil
}

func (f *fakeAgent) Health(ctx context.Context) error {
	if f.failHealth.Load() > 0 {
		f.failHealth.Add(-1)
		return errors.New("จำลองล้มเหลว")
	}
	return nil
}

// TestManagerRestartsUnhealthyAgent — health ล้ม 2 ครั้งติด → ต้องรีสตาร์ท 1 ครั้ง
// แล้วเอเจนต์กลับมาแข็งแรง รันได้ต่อ
func TestManagerRestartsUnhealthyAgent(t *testing.T) {
	m := New(log.New(io.Discard, "", 0), 20*time.Millisecond)
	m.SetBackoff(5*time.Millisecond, 20*time.Millisecond)

	f := &fakeAgent{name: "fake"}
	f.failHealth.Store(3) // รอบ 1: ล้ม 2 ครั้ง → รีสตาร์ท; รอบ 2: ล้ม 1 ครั้งแล้วปกติ
	m.Register(f)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go m.Run(ctx)

	// รอจนถูกรีสตาร์ท (Run ครั้งที่ 2)
	deadline := time.Now().Add(4 * time.Second)
	for f.runs.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("เอเจนต์ควรถูกรีสตาร์ทแล้ว แต่ Run ถูกเรียกเพียง %d ครั้ง", f.runs.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}

	// สถานะต้องกลับมา running และนับ restarts ได้ 1
	deadline = time.Now().Add(2 * time.Second)
	for {
		st := m.Status()[0]
		if st.State == StateRunning && st.Restarts == 1 {
			return // ผ่าน
		}
		if time.Now().After(deadline) {
			t.Fatalf("สถานะไม่ตรง: %+v", st)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestManagerStatus — เอเจนต์ที่ลงทะเบียนต้องปรากฏใน Status
func TestManagerStatus(t *testing.T) {
	m := New(log.New(io.Discard, "", 0), time.Second)
	m.Register(&fakeAgent{name: "a"})
	m.Register(&fakeAgent{name: "b"})

	st := m.Status()
	if len(st) != 2 || st[0].Name != "a" || st[1].Name != "b" {
		t.Fatalf("Status ไม่ครบ/ไม่ถูก: %+v", st)
	}
	if st[0].State != StateStopped {
		t.Fatalf("ก่อน Run ต้องเป็น %s แต่ได้ %s", StateStopped, st[0].State)
	}
}
