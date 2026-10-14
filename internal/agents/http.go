package agents

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

// HTTPAgent รันเว็บเซิร์ฟเวอร์ (API) พร้อมปิดอย่างสง่างามเมื่อถูกสั่ง
type HTTPAgent struct {
	addr    string // เช่น ":8080"
	handler http.Handler
	logger  *log.Logger
}

// NewHTTP สร้างเอเจนต์เว็บเซิร์ฟเวอร์
func NewHTTP(addr string, handler http.Handler, logger *log.Logger) *HTTPAgent {
	return &HTTPAgent{addr: addr, handler: handler, logger: logger}
}

func (a *HTTPAgent) Name() string { return "http" }

// Run เปิดรับ connection จนกว่า ctx จะถูกยกเลิก แล้ว shutdown ภายใน 5 วินาที
func (a *HTTPAgent) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              a.addr,
		Handler:           a.handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err // เช่นพอร์ตถูกใช้อยู่ → manager จะรีสตาร์ทรอจนกว่าจะว่าง
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a.logger.Printf("[http] กำลังปิดเซิร์ฟเวอร์...")
		return srv.Shutdown(sctx)
	}
}

// Health ลองต่อ TCP เข้าพอร์ตตัวเอง ถ้าต่อไม่ได้แปลว่าเซิร์ฟเวอร์ล่ม
func (a *HTTPAgent) Health(ctx context.Context) error {
	addr := a.addr
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("ต่อ %s ไม่ได้: %w", addr, err)
	}
	return conn.Close()
}
