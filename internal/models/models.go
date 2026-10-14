// Package models โครงสร้างข้อมูลหลักของระบบ
package models

import "time"

// User บัญชีผู้ใช้ระบบ CRM (คนทีมของเรา ไม่ใช่ลูกค้า)
type User struct {
	ID        int64      `json:"id"`
	Username  string     `json:"username"`
	Role      string     `json:"role"` // admin | viewer
	CreatedAt time.Time  `json:"created_at"`
}

// Customer ลูกค้า 1 ราย (อาจใช้งานหลาย Service)
type Customer struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Company   string    `json:"company"`  // บริษัท/หน่วยงานของลูกค้า
	Contact   string    `json:"contact"`  // ชื่อผู้ติดต่อ
	Email     string    `json:"email"`
	Phone     string    `json:"phone"`
	Address   string    `json:"address"`  // ที่อยู่
	TaxID     string    `json:"tax_id"`   // เลขประจำตัวผู้เสียภาษี
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

// Service โปรแกรม/ระบบที่ลูกค้าใช้งาน 1 รายการ
// ลูกค้า 1 รายมีได้หลาย Service แต่ละอันมี domain/user/password/วันหมดอายุของตัวเอง
type Service struct {
	ID             int64      `json:"id"`
	CustomerID     int64      `json:"customer_id"`
	ProgramName    string     `json:"program_name"` // เช่น "ERP", "POS", "Mail"
	Domain         string     `json:"domain"`
	Username       string     `json:"username"`
	PasswordEnc    string     `json:"-"` // AES-256-GCM (ไม่ส่งกลับใน list API)
	RegisteredFrom time.Time  `json:"registered_from"`
	RegisteredTo   time.Time  `json:"registered_to"`
	// เอกสารอ้างอิง + สถานะการขาย/ต่ออายุ/การเงิน
	RefInvoice   string     `json:"ref_invoice"`   // ref ใบแจ้งหนี้
	RefQuotation string     `json:"ref_quotation"` // ref ใบเสนอราคา
	Price        float64    `json:"price"`         // มูลค่า/ราคา
	AmountPaid   float64    `json:"amount_paid"`   // ยอดที่ได้รับแล้ว
	PaidDate     *time.Time `json:"paid_date"`     // วันที่ได้รับเงิน
	QuoteStatus  string     `json:"quote_status"`  // none | sent | approved
	QuoteDate    *time.Time `json:"quote_date"`    // วันที่ยื่นใบเสนอราคา
	RenewStatus  string     `json:"renew_status"`  // none | renewed
	Notes        string     `json:"notes"`
	CreatedAt    time.Time  `json:"created_at"`
}

// NotificationLog บันทึกการแจ้งเตือนที่สร้าง/ส่ง
type NotificationLog struct {
	ID        int64     `json:"id"`
	ServiceID int64     `json:"service_id"`
	EmailTo   string    `json:"email_to"`
	Type      string    `json:"type"` // expiring_30 | expiring_7 | expiring_1 | expired
	Status    string    `json:"status"` // sent | failed | simulated
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

// AuditLog ใครทำอะไรเมื่อไร
type AuditLog struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	CreatedAt time.Time `json:"created_at"`
}
