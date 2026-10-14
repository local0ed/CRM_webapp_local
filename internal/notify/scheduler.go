// Package notify ตรวจ service ใกล้หมดอายุแล้วส่งอีเมลแจ้งเตือน (self-contained ไม่พึ่ง repo)
package notify

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"crm/internal/mail"
)

// กติกา: offset วัน (เทียบวันนี้) -> ชนิดการแจ้งเตือน
var notifyTypes = []struct {
	Offset int
	Type   string
}{
	{30, "expiring_30"},
	{7, "expiring_7"},
	{1, "expiring_1"},
	{-1, "expired"},
}

// CheckAndNotify ตรวจ service ที่ใกล้หมดอายุ/หมดอายุแล้ว แล้วสร้างการแจ้งเตือน
// คืนจำนวนฉบับที่สร้าง (รวม simulated)
func CheckAndNotify(db *sql.DB, m *mail.Mailer) (int, error) {
	created := 0

	for _, nt := range notifyTypes {
		rows, err := db.Query(`
			SELECT s.id, s.program_name, s.domain, s.registered_to,
			       COALESCE(c.name, ''), COALESCE(c.email, '')
			FROM services s
			JOIN customers c ON c.id = s.customer_id
			WHERE s.registered_to::date = CURRENT_DATE + $1::int`, nt.Offset)
		if err != nil {
			return created, fmt.Errorf("ค้นหา service (offset %d): %w", nt.Offset, err)
		}

		for rows.Next() {
			var (
				id            int64
				program       string
				domain        string
				registeredTo  time.Time
				customerName  string
				customerEmail string
			)
			if err := rows.Scan(&id, &program, &domain, &registeredTo, &customerName, &customerEmail); err != nil {
				rows.Close()
				return created, fmt.Errorf("อ่านผลลัพธ์ (offset %d): %w", nt.Offset, err)
			}

			// ข้ามถ้าแจ้ง type นี้ของ service นี้ไปแล้ววันนี้
			notified, err := WasNotifiedToday(db, id, nt.Type)
			if err != nil {
				rows.Close()
				return created, fmt.Errorf("ตรวจการแจ้งเตือนเดิม (service %d): %w", id, err)
			}
			if notified {
				continue
			}

			to := customerEmail
			if to == "" {
				to = m.MailFrom() // ไม่มีอีเมลลูกค้า ส่งภายในแทน
			}

			status, detail, err := sendNotification(m, to, nt.Type, program, domain, registeredTo, nt.Offset)

			// บันทึกทุกครั้งลง notifications ไม่ว่าจะส่งสำเร็จหรือไม่
			if _, err := db.Exec(`
				INSERT INTO notifications (service_id, email_to, type, status, detail, created_at)
				VALUES ($1, $2, $3, $4, $5, now())`,
				id, to, nt.Type, status, detail); err != nil {
				rows.Close()
				return created, fmt.Errorf("บันทึกการแจ้งเตือน (service %d): %w", id, err)
			}
			if err != nil {
				rows.Close()
				return created, fmt.Errorf("ส่งอีเมล (service %d): %w", id, err)
			}
			created++
		}
		if err := rows.Err(); err != nil {
			return created, err
		}
		rows.Close()
	}
	return created, nil
}

// sendNotification จัดการส่งเมล คืน (status, detail, error)
func sendNotification(m *mail.Mailer, to, ntype, program, domain string, registeredTo time.Time, daysLeft int) (string, string, error) {
	subject := fmt.Sprintf("[CRM] แจ้งเตือน: %s — %s", ntype, program)
	body := buildBody(program, domain, registeredTo, daysLeft)

	status, err := m.Send(to, subject, body)
	if err != nil {
		detail := fmt.Sprintf("ส่งถึง %s ล้มเหลว: %v", to, err)
		if len(detail) > 500 {
			detail = detail[:500]
		}
		return status, detail, err
	}
	return status, fmt.Sprintf("ส่งถึง %s เรียบร้อย", to), nil
}

// buildBody สร้างเนื้อหาอีเมลภาษาไทย
func buildBody(program, domain string, registeredTo time.Time, daysLeft int) string {
	expiry := registeredTo.Format("02 Jan 2006")
	var heading, remaining string
	if daysLeft > 0 {
		heading = "โปรแกรมที่คุณใช้งานใกล้หมดอายุ"
		remaining = fmt.Sprintf("เหลืออีก %d วัน", daysLeft)
	} else {
		heading = "โปรแกรมที่คุณใช้งานหมดอายุแล้ว"
		remaining = "หมดอายุไปแล้ว 1 วัน"
	}
	return fmt.Sprintf(
		heading+"\n\n"+
			"  โปรแกรม: %s\n"+
			"  Domain: %s\n"+
			"  หมดอายุวันที่: %s\n"+
			"  สถานะ: %s\n\n"+
			"กรุณาติดต่อเพื่อต่ออายุการใช้งาน\n",
		program, domain, expiry, remaining)
}

// WasNotifiedToday ตรวจว่าเคยแจ้ง type นี้ของ service นี้วันนี้หรือยัง
func WasNotifiedToday(db *sql.DB, serviceID int64, ntype string) (bool, error) {
	var n int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM notifications
		WHERE service_id = $1 AND type = $2 AND created_at::date = CURRENT_DATE`,
		serviceID, ntype).Scan(&n)
	return n > 0, err
}

// StartScheduler รัน CheckAndNotify ครั้งแรกตอนเริ่ม แล้วทำซ้ำทุก 24 ชั่วโมง
func StartScheduler(db *sql.DB, m *mail.Mailer) {
	go func() {
		for {
			if n, err := CheckAndNotify(db, m); err != nil {
				log.Printf("[notify] ผิดพลาด: %v (สร้างไปแล้ว %d ฉบับ)", err, n)
			} else {
				log.Printf("[notify] สร้างการแจ้งเตือน %d ฉบับ", n)
			}
			time.Sleep(24 * time.Hour)
		}
	}()
}
