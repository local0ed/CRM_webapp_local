package agents

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// JournalEntry เหตุการณ์ 1 รายการในสมุดบันทึก
type JournalEntry struct {
	Time   time.Time `json:"time"`
	Kind   string    `json:"kind"`   // เช่น boot, shutdown, manager, api, notify
	Detail string    `json:"detail"` // รายละเอียดสั้น ๆ
}

// JournalAgent เก็บ "สิ่งที่ระบบและเราทำ" ลงไฟล์ JSONL (append-only)
// เขียนทันทีที่ Append + fsync เป็นรอบ เพื่อให้กลับมาต่องานได้แม้เครื่องดับกลางทาง
// ไฟล์อยู่ที่ path ที่กำหนด (default data/journal.jsonl)
type JournalAgent struct {
	path   string
	logger *log.Logger

	mu      sync.Mutex
	f       *os.File
	tail    []JournalEntry // รายการล่าสุด (in-memory) ไว้แสดงใน API
	tailMax int
}

// NewJournal สร้างเอเจนต์สมุดบันทึก; ถ้ามีไฟล์เดิมอยู่จะเก็บต่อท้าย (append)
// พร้อมโหลดรายการเก่าล่าสุด 100 ฉบับไว้ในหน่วยความจำให้ API อ่านย้อนหลังได้ทันที
func NewJournal(path string, logger *log.Logger) (*JournalAgent, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	j := &JournalAgent{path: path, logger: logger, tailMax: 100}

	if entries, err := ReadJournal(path, j.tailMax); err == nil {
		j.tail = entries
	} else {
		logger.Printf("[journal] อ่านสมุดเดิมไม่ได้ (%v) — เริ่มใหม่", err)
	}
	return j, nil
}

func (j *JournalAgent) Name() string { return "journal" }

// Run เปิดไฟล์ แล้ว fsync เป็นรอบเพื่อให้ข้อมูลอยู่รอดแม้ไฟดับ
func (j *JournalAgent) Run(ctx context.Context) error {
	f, err := os.OpenFile(j.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	j.mu.Lock()
	j.f = f
	j.mu.Unlock()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			j.mu.Lock()
			f.Sync()
			f.Close()
			j.f = nil
			j.mu.Unlock()
			return nil
		case <-ticker.C:
			j.mu.Lock()
			if j.f != nil {
				f.Sync()
			}
			j.mu.Unlock()
		}
	}
}

// Health ตรวจว่าไฟล์สมุดยังเขียนได้
func (j *JournalAgent) Health(ctx context.Context) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return os.ErrNotExist
	}
	info, err := j.f.Stat()
	if err != nil {
		return err
	}
	if info.Mode()&0o200 == 0 {
		return os.ErrPermission
	}
	return nil
}

// Append บันทึกเหตุการณ์ทันที (thread-safe); ใช้จากทุกส่วนของระบบ
// เปิดไฟล์ให้เองถ้ายังไม่เปิด (ใช้ได้ทั้งก่อน Run และหลัง Run ปิดไปแล้ว)
func (j *JournalAgent) Append(kind, detail string) {
	e := JournalEntry{Time: time.Now(), Kind: kind, Detail: detail}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	b = append(b, '\n')

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		if f, err := os.OpenFile(j.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			j.f = f
		}
	}
	if j.f != nil {
		j.f.Write(b)
	}
	j.tail = append(j.tail, e)
	if len(j.tail) > j.tailMax {
		j.tail = j.tail[len(j.tail)-j.tailMax:]
	}
}

// Close sync + ปิดไฟล์สมุด (เรียกตอน shutdown หลังบันทึกรายการสุดท้าย)
func (j *JournalAgent) Close() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f != nil {
		j.f.Sync()
		j.f.Close()
		j.f = nil
	}
}

// Last คืนรายการล่าสุด n ฉบับ (จากหน่วยความจำ)
func (j *JournalAgent) Last(n int) []JournalEntry {
	if n < 1 {
		n = 20
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.tail) > n {
		start := len(j.tail) - n
		out := make([]JournalEntry, n)
		copy(out, j.tail[start:])
		return out
	}
	out := make([]JournalEntry, len(j.tail))
	copy(out, j.tail)
	return out
}

// ReadJournal อ่านรายการล่าสุด n ฉบับจากไฟล์สมุด (ใช้ตอนบูตเพื่อ "กลับมาต่องาน")
func ReadJournal(path string, n int) ([]JournalEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var all []JournalEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e JournalEntry
		if err := json.Unmarshal(line, &e); err == nil {
			all = append(all, e)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}
