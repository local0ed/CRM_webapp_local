// Package manager ผู้ควบคุมกลาง (supervisor) ที่รันเอเจนต์ทุกตัวเป็น goroutine
// ตรวจสุขภาพเป็นระยะ ถ้าเอเจนต์ตัวใดตายหรือไม่แข็งแรง จะรีสตาร์ทให้เอง
// พร้อมรายงานสถานะรวมสำหรับ /healthz และส่งเหตุการณ์ต่อให้ event sink (เช่น journal)
package manager

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"crm/internal/agent"
)

// maxConsecFails จำนวนครั้งที่ health check ล้มเหลวติดกันก่อนสั่งรีสตาร์ท
const maxConsecFails = 2

// สถานะเอเจนต์ (เปิดเป็น public ให้ API/healthz อ่านได้)
const (
	StateRunning    = "running"
	StateRestarting = "restarting"
	StateStopped    = "stopped"
)

// Agent — alias สั้น ๆ (นิยามจริงอยู่ใน package agent)
type Agent = agent.Agent

// Status สถานะปัจจุบันของเอเจนต์ 1 ตัว (สำหรับ /healthz)
type Status struct {
	Name      string    `json:"name"`
	State     string    `json:"state"`
	Restarts  int       `json:"restarts"`
	LastError string    `json:"last_error,omitempty"`
	LastCheck time.Time `json:"last_check,omitempty"`
}

// worker คือเอเจนต์ 1 ตัวที่ manager ดูแล พร้อมสถานะล่าสุด
type worker struct {
	a         Agent
	mu        sync.Mutex
	state     string
	restarts  int
	lastError string
	lastCheck time.Time
}

func (w *worker) setState(state, lastErr string) {
	w.mu.Lock()
	w.state = state
	if lastErr != "" {
		w.lastError = lastErr
	}
	w.mu.Unlock()
}

func (w *worker) recordCheck(err error) {
	w.mu.Lock()
	w.lastCheck = time.Now()
	if err != nil {
		w.lastError = err.Error()
	}
	w.mu.Unlock()
}

func (w *worker) snapshot() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return Status{
		Name:      w.a.Name(),
		State:     w.state,
		Restarts:  w.restarts,
		LastError: w.lastError,
		LastCheck: w.lastCheck,
	}
}

// Manager ครอบรายการเอเจนต์ทั้งหมดและดูแลให้รันตลอดเวลา
type Manager struct {
	logger       *log.Logger
	interval     time.Duration // รอบตรวจสุขภาพ
	startBackoff time.Duration // ดีเลย์รอบแรกก่อนรีสตาร์ท
	maxBackoff   time.Duration

	sinkMu sync.Mutex
	sink   func(kind, detail string) // event sink เช่น journal (อาจเป็น nil)

	mu      sync.Mutex
	workers []*worker
}

// New สร้าง manager; healthInterval คือรอบตรวจสุขภาพ (0 = 15 วินาที)
func New(logger *log.Logger, healthInterval time.Duration) *Manager {
	if logger == nil {
		logger = log.Default()
	}
	if healthInterval <= 0 {
		healthInterval = 15 * time.Second
	}
	return &Manager{
		logger:       logger,
		interval:     healthInterval,
		startBackoff: 2 * time.Second,
		maxBackoff:   30 * time.Second,
	}
}

// SetBackoff ปรับดีเลย์รีสตาร์ท (เริ่มต้น, สูงสุด)
func (m *Manager) SetBackoff(start, max time.Duration) {
	m.startBackoff, m.maxBackoff = start, max
}

// SetEventSink ตั้งฟังก์ชันรับเหตุการณ์ของ manager (เช่นเก็บลง journal); nil = ปิด
func (m *Manager) SetEventSink(fn func(kind, detail string)) {
	m.sinkMu.Lock()
	m.sink = fn
	m.sinkMu.Unlock()
}

func (m *Manager) emit(kind, format string, args ...interface{}) {
	detail := fmt.Sprintf(format, args...)
	m.logger.Printf("[%s] %s", kind, detail)
	m.sinkMu.Lock()
	fn := m.sink
	m.sinkMu.Unlock()
	if fn != nil {
		fn(kind, detail)
	}
}

// Register ลงทะเบียนเอเจนต์ (เรียกก่อน Run)
func (m *Manager) Register(a Agent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workers = append(m.workers, &worker{a: a, state: StateStopped})
}

// Run สตาร์ทเอเจนต์ทุกตัวแล้วบล็อกรอ จนกว่า ctx จะถูกยกเลิก (เช่นกด Ctrl+C)
// แล้วรอให้ทุกตัวหยุดอย่างสง่างาม
func (m *Manager) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, w := range m.workers {
		wg.Add(1)
		go func(w *worker) {
			defer wg.Done()
			m.supervise(ctx, w)
		}(w)
	}
	<-ctx.Done()
	m.emit("manager", "ปิดระบบ... รอเอเจนต์ทั้งหมดหยุดอย่างสง่างาม")
	wg.Wait()
	m.mu.Lock()
	for _, w := range m.workers {
		w.setState(StateStopped, "")
	}
	m.mu.Unlock()
}

// supervise ดูแลเอเจนต์ตัวเดียว: รัน → ตรวจสุขภาพ → รีสตาร์ทถ้าจำเป็น (วนไปเรื่อย ๆ)
func (m *Manager) supervise(parent context.Context, w *worker) {
	a := w.a
	backoff := m.startBackoff

	for {
		if parent.Err() != nil {
			return
		}

		runCtx, cancel := context.WithCancel(parent)
		done := make(chan error, 1)
		go func() { done <- a.Run(runCtx) }()
		w.setState(StateRunning, "")
		m.emit("manager", "เริ่มเอเจนต์ %s", a.Name())

		fails := 0
		ticker := time.NewTicker(m.interval)
	check:
		for {
			select {
			case err := <-done:
				// เอเจนต์หยุดเอง (ตายหรือถูกยกเลิก)
				cancel()
				if parent.Err() != nil {
					return // manager ปิดระบบอยู่ ไม่ต้องรีสตาร์ท
				}
				if err != nil {
					m.emit("manager", "เอเจนต์ %s ดับ: %v — จะรีสตาร์ท", a.Name(), err)
				} else {
					m.emit("manager", "เอเจนต์ %s หยุดเองกลางทาง — จะรีสตาร์ท", a.Name())
				}
				w.setState(StateRestarting, errString(err))
				break check

			case <-ticker.C:
				err := a.Health(runCtx)
				w.recordCheck(err)
				if err != nil {
					fails++
					m.emit("manager", "%s ตรวจสุขภาพไม่ผ่าน (%d/%d): %v", a.Name(), fails, maxConsecFails, err)
					if fails >= maxConsecFails {
						// สั่งให้เอเจนต์หยุดตัวเองแล้วรอสักครู่ ก่อนรีสตาร์ท
						m.emit("manager", "%s ไม่แข็งแรงติดกัน %d ครั้ง — สั่งรีสตาร์ท", a.Name(), fails)
						cancel()
						select {
						case <-done: // หยุดเรียบร้อย
						case <-time.After(5 * time.Second):
							m.emit("manager", "%s หยุดตัวเองไม่สำเร็จภายใน 5 วินาที", a.Name())
						}
						w.setState(StateRestarting, err.Error())
						break check
					}
				} else {
					fails = 0
				}
			}
		}
		ticker.Stop()

		m.mu.Lock()
		w.restarts++
		m.mu.Unlock()

		select {
		case <-parent.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < m.maxBackoff {
			backoff *= 2
		}
	}
}

// Status คืนสถานะล่าสุดของเอเจนต์ทุกตัว (สำหรับแสดงใน /healthz)
func (m *Manager) Status() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.workers))
	for _, w := range m.workers {
		out = append(out, w.snapshot())
	}
	return out
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
