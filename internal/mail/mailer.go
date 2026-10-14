// Package mail ผู้ช่วยส่งอีเมล (รองรับ dry-run สำหรับเดโม)
package mail

import (
	"crypto/tls"
	"fmt"
	"mime"
	"net/smtp"
	"time"

	"crm/internal/config"
)

// Mailer เก็บ config สำหรับส่งเมล
type Mailer struct {
	cfg *config.Config
}

// New สร้าง Mailer จาก config
func New(cfg *config.Config) *Mailer {
	return &Mailer{cfg: cfg}
}

// MailFrom คืนอีเมลผู้ส่ง (ใช้เป็นที่อยู่สำรองสำหรับเมลภายใน)
func (m *Mailer) MailFrom() string {
	return m.cfg.MailFrom
}

// Send ส่งอีเมล 1 ฉบับ
// - MailDryRun: จำลองการส่ง (log stdout) คืน status "simulated"
// - ส่งจริง: SMTP STARTTLS + AUTH ลองสูงสุด 3 ครั้ง ห่างกัน 2 วินาที
func (m *Mailer) Send(to, subject, body string) (status string, err error) {
	if m.cfg.MailDryRun {
		fmt.Printf("[MAIL DRY-RUN] to=%s subject=%s\n", to, subject)
		return "simulated", nil
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if err := m.sendOnce(to, subject, body); err != nil {
			lastErr = err
			if attempt < 3 {
				time.Sleep(2 * time.Second)
			}
			continue
		}
		return "sent", nil
	}
	return "failed", fmt.Errorf("ส่งอีเมลล้มเหลวหลังลอง 3 ครั้ง: %w", lastErr)
}

// sendOnce ส่งครั้งเดียวผ่าน net/smtp
func (m *Mailer) sendOnce(to, subject, body string) error {
	addr := fmt.Sprintf("%s:%d", m.cfg.SMTPHost, m.cfg.SMTPPort)

	c, err := smtp.Dial(addr)
	if err != nil {
		return err
	}
	defer c.Close()

	// อัปเกรดเป็น TLS ถ้าเซิร์ฟเวอร์รองรับ
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: m.cfg.SMTPHost}); err != nil {
			return err
		}
	}

	// เข้าสู่ระบบเมื่อมี SMTPUser
	if m.cfg.SMTPUser != "" {
		auth := smtp.PlainAuth("", m.cfg.SMTPUser, m.cfg.SMTPPass, m.cfg.SMTPHost)
		if err := c.Auth(auth); err != nil {
			return err
		}
	}

	if err := c.Mail(m.cfg.MailFrom); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}

	w, err := c.Data()
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=\"UTF-8\"\r\n\r\n%s\r\n",
		m.cfg.MailFrom, to, mime.QEncoding.Encode("UTF-8", subject), body)
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
