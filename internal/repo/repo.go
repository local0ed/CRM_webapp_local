// Package repo ชั้นเข้าถึงข้อมูล (data layer) ของ CRM
package repo

import (
	"database/sql"
	"strings"
	"time"

	"crm/internal/models"
)

// Store ครอบ *sql.DB สำหรับทุกเมธอด query
type Store struct {
	DB *sql.DB
}

// ServiceView — แถวสำหรับแสดงผลรวม customers+services
type ServiceView struct {
	ID             int64     `json:"id"`
	CustomerID     int64     `json:"customer_id"`
	CustomerName   string    `json:"customer_name"`
	Email          string    `json:"email"`
	ProgramName    string    `json:"program_name"`
	Domain         string    `json:"domain"`
	Username       string    `json:"username"`
	RegisteredFrom time.Time `json:"registered_from"`
	RegisteredTo   time.Time `json:"registered_to"`
	DaysLeft       *int      `json:"days_left"` // วันจนถึง registered_to (ติดลบถ้าหมดอายุ); nil = ยังไม่กำหนดวันหมดอายุ
	RefInvoice     string    `json:"ref_invoice"`
	RefQuotation   string    `json:"ref_quotation"`
	Price          float64   `json:"price"`
	AmountPaid     float64   `json:"amount_paid"`
	PaidDate       *time.Time `json:"paid_date"`
	QuoteStatus    string    `json:"quote_status"`
	QuoteDate      *time.Time `json:"quote_date"`
	RenewStatus    string    `json:"renew_status"`
	Notes          string    `json:"notes"`
}

// daysUntil คำนวณจำนวนวันจนถึงวันที่ t (เทียบแบบวันที่ ไม่สนเวลา)
func daysUntil(t time.Time) int {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	return int(t.UTC().Truncate(24*time.Hour).Sub(today).Hours() / 24)
}

// daysPtr คำนวณ daysUntil แล้วคืนเป็น pointer (nil ต่างหากสำหรับรายการที่ไม่มีวันหมดอายุ)
func daysPtr(t time.Time) *int {
	d := daysUntil(t)
	return &d
}

const serviceViewSelect = `
	SELECT s.id, s.customer_id, c.name, c.email, s.program_name, s.domain, s.username,
	       s.registered_from, s.registered_to,
	       s.ref_invoice, s.ref_quotation, s.price, s.amount_paid, s.paid_date,
	       s.quote_status, s.quote_date, s.renew_status, s.notes
	FROM services s
	JOIN customers c ON c.id = s.customer_id
`

// scannable ครอบสิ่งที่มี Scan(dest ...any) error (Row และ Rows ทั้งคู่)
type scannable interface{ Scan(dest ...any) error }

// scanServiceViewRow scan แถวเดียวเป็น ServiceView (คำนวณ DaysLeft ใน Go)
func scanServiceViewRow(row scannable) (ServiceView, error) {
	var v ServiceView
	var from, to, paid, qdate sql.NullTime
	err := row.Scan(&v.ID, &v.CustomerID, &v.CustomerName, &v.Email,
		&v.ProgramName, &v.Domain, &v.Username, &from, &to,
		&v.RefInvoice, &v.RefQuotation, &v.Price, &v.AmountPaid, &paid,
		&v.QuoteStatus, &qdate, &v.RenewStatus, &v.Notes)
	if err != nil {
		return v, err
	}
	if from.Valid {
		v.RegisteredFrom = from.Time
	}
	if to.Valid {
		v.RegisteredTo = to.Time
		v.DaysLeft = daysPtr(to.Time)
	}
	if paid.Valid {
		v.PaidDate = &paid.Time
	}
	if qdate.Valid {
		v.QuoteDate = &qdate.Time
	}
	return v, nil
}

// ListServices ค้นหา + กรองตามสถานะ + pagination แบบ server-side
// status: "all" | "active" | "expiring" (<=30 วันและยังไม่หมด) | "expired"
// คืนรายการ + จำนวนทั้งหมด (สำหรับ pagination)
func (s *Store) ListServices(query, status string, page, pageSize int) ([]ServiceView, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}

	conds := []string{}
	args := []interface{}{}
	arg := func(v interface{}) string {
		args = append(args, v)
		return "$" + itoa(len(args))
	}
	if q := strings.TrimSpace(query); q != "" {
		like := "%" + q + "%"
		conds = append(conds, "(c.name ILIKE "+arg(like)+" OR s.domain ILIKE "+arg(like)+
			" OR s.program_name ILIKE "+arg(like)+" OR s.username ILIKE "+arg(like)+
			" OR s.ref_invoice ILIKE "+arg(like)+" OR s.ref_quotation ILIKE "+arg(like)+")")
	}
	switch status {
	case "active":
		conds = append(conds, "s.registered_to >= CURRENT_DATE")
	case "expiring":
		// ยังไม่หมดอายุ และเหลือ <= 30 วัน
		conds = append(conds, "s.registered_to >= CURRENT_DATE AND s.registered_to <= CURRENT_DATE + 30")
	case "expired":
		conds = append(conds, "s.registered_to < CURRENT_DATE")
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	// นับจำนวนทั้งหมดก่อน
	var total int
	if err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM services s JOIN customers c ON c.id = s.customer_id `+where,
		args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	// ดึงหน้าที่ต้องการ
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := s.DB.Query(
		serviceViewSelect+where+`
		ORDER BY s.registered_to ASC NULLS LAST, s.id ASC
		LIMIT $`+itoa(len(args)-1)+` OFFSET $`+itoa(len(args)),
		args...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []ServiceView{}
	for rows.Next() {
		v, err := scanServiceViewRow(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}

// GetEncryptedPassword คืน password_enc ของ service
func (s *Store) GetEncryptedPassword(serviceID int64) (string, error) {
	var enc string
	err := s.DB.QueryRow(`SELECT password_enc FROM services WHERE id = $1`, serviceID).Scan(&enc)
	return enc, err
}

// GetService คืน service รายตัวพร้อม customer_name (สำหรับหน้า detail)
func (s *Store) GetService(id int64) (ServiceView, error) {
	return scanServiceViewRow(s.DB.QueryRow(serviceViewSelect+`WHERE s.id = $1`, id))
}

// UpdateService แก้ไข service รายตัว; ถ้า PasswordEnc == "" ให้คงรหัสเดิมไว้
func (s *Store) UpdateService(id int64, svc *models.Service) error {
	if svc.PasswordEnc == "" {
		var enc string
		if err := s.DB.QueryRow(`SELECT password_enc FROM services WHERE id = $1`, id).Scan(&enc); err != nil {
			return err
		}
		svc.PasswordEnc = enc
	}
	var from, to, paid, qdate interface{}
	if !svc.RegisteredFrom.IsZero() {
		from = svc.RegisteredFrom
	}
	if !svc.RegisteredTo.IsZero() {
		to = svc.RegisteredTo
	}
	if svc.PaidDate != nil {
		paid = *svc.PaidDate
	}
	if svc.QuoteDate != nil {
		qdate = *svc.QuoteDate
	}
	_, err := s.DB.Exec(
		`UPDATE services SET program_name = $2, domain = $3, username = $4, password_enc = $5,
		 registered_from = $6, registered_to = $7, ref_invoice = $8, ref_quotation = $9,
		 price = $10, amount_paid = $11, paid_date = $12, quote_status = $13, quote_date = $14,
		 renew_status = $15, notes = $16
		 WHERE id = $1`,
		id, svc.ProgramName, svc.Domain, svc.Username, svc.PasswordEnc, from, to,
		svc.RefInvoice, svc.RefQuotation, svc.Price, svc.AmountPaid, paid,
		svc.QuoteStatus, qdate, svc.RenewStatus, svc.Notes,
	)
	return err
}

// GetCustomerWithServices คืน customer + รายการ service ทั้งหมด (สำหรับหน้าแก้ไข)
func (s *Store) GetCustomerWithServices(id int64) (*models.Customer, []models.Service, error) {
	c := &models.Customer{}
	err := s.DB.QueryRow(
		`SELECT id, name, company, contact, email, phone, address, tax_id, note, created_at
		 FROM customers WHERE id = $1`, id,
	).Scan(&c.ID, &c.Name, &c.Company, &c.Contact, &c.Email, &c.Phone,
		&c.Address, &c.TaxID, &c.Note, &c.CreatedAt)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.DB.Query(
		`SELECT id, customer_id, program_name, domain, username, password_enc,
		        registered_from, registered_to, created_at,
		        ref_invoice, ref_quotation, price, amount_paid, paid_date,
		        quote_status, quote_date, renew_status, notes
		 FROM services WHERE customer_id = $1 ORDER BY id ASC`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	svcs := []models.Service{}
	for rows.Next() {
		var sv models.Service
		var from, to, paid, qdate sql.NullTime
		if err := rows.Scan(&sv.ID, &sv.CustomerID, &sv.ProgramName, &sv.Domain,
			&sv.Username, &sv.PasswordEnc, &from, &to, &sv.CreatedAt,
			&sv.RefInvoice, &sv.RefQuotation, &sv.Price, &sv.AmountPaid, &paid,
			&sv.QuoteStatus, &qdate, &sv.RenewStatus, &sv.Notes); err != nil {
			return nil, nil, err
		}
		if from.Valid {
			sv.RegisteredFrom = from.Time
		}
		if to.Valid {
			sv.RegisteredTo = to.Time
		}
		if paid.Valid {
			sv.PaidDate = &paid.Time
		}
		if qdate.Valid {
			sv.QuoteDate = &qdate.Time
		}
		svcs = append(svcs, sv)
	}
	return c, svcs, rows.Err()
}

// CreateCustomerWithServices สร้าง customer + services พร้อมกันใน transaction เดียว
func (s *Store) CreateCustomerWithServices(c *models.Customer, svcs []models.Service) (int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	err = tx.QueryRow(
		`INSERT INTO customers (name, company, contact, email, phone, address, tax_id, note)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		c.Name, c.Company, c.Contact, c.Email, c.Phone, c.Address, c.TaxID, c.Note,
	).Scan(&c.ID)
	if err != nil {
		return 0, err
	}
	if err := insertServices(tx, svcs, c.ID); err != nil {
		return 0, err
	}
	return c.ID, tx.Commit()
}

// insertServices INSERT รายการ services ทั้งหมดผูกกับ customerID
func insertServices(tx *sql.Tx, svcs []models.Service, customerID int64) error {
	for i := range svcs {
		sv := &svcs[i]
		var from, to, paid, qdate interface{}
		if !sv.RegisteredFrom.IsZero() {
			from = sv.RegisteredFrom
		}
		if !sv.RegisteredTo.IsZero() {
			to = sv.RegisteredTo
		}
		if sv.PaidDate != nil {
			paid = *sv.PaidDate
		}
		if sv.QuoteDate != nil {
			qdate = *sv.QuoteDate
		}
		err := tx.QueryRow(
			`INSERT INTO services (customer_id, program_name, domain, username, password_enc,
				registered_from, registered_to, ref_invoice, ref_quotation, price, amount_paid,
				paid_date, quote_status, quote_date, renew_status, notes)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16) RETURNING id`,
			customerID, sv.ProgramName, sv.Domain, sv.Username, sv.PasswordEnc, from, to,
			sv.RefInvoice, sv.RefQuotation, sv.Price, sv.AmountPaid, paid,
			sv.QuoteStatus, qdate, sv.RenewStatus, sv.Notes,
		).Scan(&sv.ID)
		if err != nil {
			return err
		}
		sv.CustomerID = customerID
	}
	return nil
}

// UpdateCustomer แก้ข้อมูล customer และ "แทนที่" รายการ services ทั้งหมด
// ถ้า svcs[i].PasswordEnc == "" ให้คงรหัสเดิมไว้ (จับคู่ตาม ID เดิม)
func (s *Store) UpdateCustomer(c *models.Customer, svcs []models.Service) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// ดึงรหัสเดิมไว้ก่อนลบ (จับคู่ตาม ID)
	old := map[int64]string{}
	rows, err := tx.Query(`SELECT id, password_enc FROM services WHERE customer_id = $1`, c.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var enc string
		if err := rows.Scan(&id, &enc); err != nil {
			rows.Close()
			return err
		}
		old[id] = enc
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	// แทนที่ services: DELETE แล้ว INSERT
	if _, err := tx.Exec(`DELETE FROM services WHERE customer_id = $1`, c.ID); err != nil {
		return err
	}
	for i := range svcs {
		sv := &svcs[i]
		if sv.PasswordEnc == "" {
			// คงรหัสเดิมไว้ถ้ามี
			if enc, ok := old[sv.ID]; ok {
				sv.PasswordEnc = enc
			}
		}
		var from, to, paid, qdate interface{}
		if !sv.RegisteredFrom.IsZero() {
			from = sv.RegisteredFrom
		}
		if !sv.RegisteredTo.IsZero() {
			to = sv.RegisteredTo
		}
		if sv.PaidDate != nil {
			paid = *sv.PaidDate
		}
		if sv.QuoteDate != nil {
			qdate = *sv.QuoteDate
		}
		err := tx.QueryRow(
			`INSERT INTO services (customer_id, program_name, domain, username, password_enc,
				registered_from, registered_to, ref_invoice, ref_quotation, price, amount_paid,
				paid_date, quote_status, quote_date, renew_status, notes)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16) RETURNING id`,
			c.ID, sv.ProgramName, sv.Domain, sv.Username, sv.PasswordEnc, from, to,
			sv.RefInvoice, sv.RefQuotation, sv.Price, sv.AmountPaid, paid,
			sv.QuoteStatus, qdate, sv.RenewStatus, sv.Notes,
		).Scan(&sv.ID)
		if err != nil {
			return err
		}
	}

	_, err = tx.Exec(`UPDATE customers SET name = $2, company = $3, contact = $4, email = $5,
		phone = $6, address = $7, tax_id = $8, note = $9 WHERE id = $1`,
		c.ID, c.Name, c.Company, c.Contact, c.Email, c.Phone, c.Address, c.TaxID, c.Note)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteCustomer ลบ customer (services ถูกลบตามด้วย ON DELETE CASCADE)
func (s *Store) DeleteCustomer(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM customers WHERE id = $1`, id)
	return err
}

// GetExpiring — service ที่ registered_to ตรงกับวันนี้+offsetDays
// (offsetDays = 1 คือพรุ่งนี้หมดอายุ, 0 คือวันนี้, -1 คือหมดอายุไปแล้ว 1 วัน)
func (s *Store) GetExpiring(offsetDays int) ([]ServiceView, error) {
	rows, err := s.DB.Query(
		serviceViewSelect+`WHERE s.registered_to = CURRENT_DATE + ($1::int)
		ORDER BY s.id ASC`,
		offsetDays,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ServiceView{}
	for rows.Next() {
		v, err := scanServiceViewRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ProgramCount ยอดบริการแยกตามโปรแกรม (สำหรับรายงาน)
type ProgramCount struct {
	Program string `json:"program"`
	Count   int    `json:"count"`
}

// ReportSummary ข้อมูลสรุปสำหรับหน้ารายงาน
// Financial สรุปยอดเงิน (สำหรับรายงาน)
type Financial struct {
	Invoiced    float64 `json:"invoiced"`    // ยอดเรียกเก็บ (SUM price)
	Collected   float64 `json:"collected"`   // ยอดที่ได้รับแล้ว (SUM amount_paid)
	Outstanding float64 `json:"outstanding"` // ค้างชำระ = invoiced - collected
}

type ReportSummary struct {
	TotalServices    int            `json:"total_services"`
	ActiveServices   int            `json:"active_services"`   // ยังใช้ได้ (รวมที่กำลังจะหมดอายุ)
	ExpiringServices int            `json:"expiring_services"` // ยังไม่หมด เหลือ <= 30 วัน
	ExpiredServices  int            `json:"expired_services"`
	NoExpiryServices int            `json:"no_expiry_services"` // ยังไม่กำหนดวันหมดอายุ
	TotalCustomers   int            `json:"total_customers"`
	Programs         []ProgramCount `json:"programs"`
	Financial        Financial      `json:"financial"`
}

// GetReportSummary นับข้อมูลสรุป + การใช้บริการแยกตามโปรแกรม (สำหรับหน้ารายงาน)
func (s *Store) GetReportSummary() (ReportSummary, error) {
	var r ReportSummary
	one := func(q string, dst *int) error {
		return s.DB.QueryRow(q).Scan(dst)
	}
	if err := one(`SELECT COUNT(*) FROM services`, &r.TotalServices); err != nil {
		return r, err
	}
	if err := one(`SELECT COUNT(*) FROM services WHERE registered_to >= CURRENT_DATE`, &r.ActiveServices); err != nil {
		return r, err
	}
	if err := one(`SELECT COUNT(*) FROM services WHERE registered_to >= CURRENT_DATE AND registered_to <= CURRENT_DATE + 30`, &r.ExpiringServices); err != nil {
		return r, err
	}
	if err := one(`SELECT COUNT(*) FROM services WHERE registered_to < CURRENT_DATE`, &r.ExpiredServices); err != nil {
		return r, err
	}
	if err := one(`SELECT COUNT(*) FROM services WHERE registered_to IS NULL`, &r.NoExpiryServices); err != nil {
		return r, err
	}
	if err := one(`SELECT COUNT(*) FROM customers`, &r.TotalCustomers); err != nil {
		return r, err
	}
	if err := s.DB.QueryRow(
		`SELECT COALESCE(SUM(price),0), COALESCE(SUM(amount_paid),0) FROM services`,
	).Scan(&r.Financial.Invoiced, &r.Financial.Collected); err != nil {
		return r, err
	}
	r.Financial.Outstanding = r.Financial.Invoiced - r.Financial.Collected

	rows, err := s.DB.Query(
		`SELECT NULLIF(program_name, ''), COUNT(*) FROM services GROUP BY 1 ORDER BY 2 DESC, 1 ASC`,
	)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	r.Programs = []ProgramCount{}
	for rows.Next() {
		var p ProgramCount
		if err := rows.Scan(&p.Program, &p.Count); err != nil {
			return r, err
		}
		r.Programs = append(r.Programs, p)
	}
	return r, rows.Err()
}

// ExpiringWithin คืน service ที่ยังไม่หมดอายุและจะหมดอายุภายในอีก days วัน
// (registered_to อยู่ระหว่างวันนี้ ถึง วันนี้+days)
func (s *Store) ExpiringWithin(days int) ([]ServiceView, error) {
	if days < 1 {
		days = 30
	}
	rows, err := s.DB.Query(
		serviceViewSelect+`WHERE s.registered_to >= CURRENT_DATE AND s.registered_to <= CURRENT_DATE + ($1::int)
		ORDER BY s.registered_to ASC, s.id ASC`,
		days,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ServiceView{}
	for rows.Next() {
		v, err := scanServiceViewRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// WasNotifiedToday true ถ้าวันนี้เคยแจ้งเตือน service นี้ด้วย type นี้แล้ว
func (s *Store) WasNotifiedToday(serviceID int64, ntype string) (bool, error) {
	var ok bool
	err := s.DB.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM notifications
		 WHERE service_id = $1 AND type = $2 AND created_at::date = CURRENT_DATE)`,
		serviceID, ntype,
	).Scan(&ok)
	return ok, err
}

// RecordNotification บันทึกการแจ้งเตือน 1 รายการ
func (s *Store) RecordNotification(n *models.NotificationLog) error {
	return s.DB.QueryRow(
		`INSERT INTO notifications (service_id, email_to, type, status, detail)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at`,
		n.ServiceID, n.EmailTo, n.Type, n.Status, n.Detail,
	).Scan(&n.ID, &n.CreatedAt)
}

// ListNotifications ประวัติการแจ้งเตือนล่าสุด
func (s *Store) ListNotifications(limit int) ([]models.NotificationLog, error) {
	if limit < 1 {
		limit = 50
	}
	rows, err := s.DB.Query(
		`SELECT id, service_id, email_to, type, status, detail, created_at
		 FROM notifications ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.NotificationLog{}
	for rows.Next() {
		var n models.NotificationLog
		if err := rows.Scan(&n.ID, &n.ServiceID, &n.EmailTo, &n.Type, &n.Status, &n.Detail, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// RecordAudit บันทึกว่าใครทำอะไรกับ target อะไร
func (s *Store) RecordAudit(username, action, target string) error {
	_, err := s.DB.Exec(
		`INSERT INTO audit_log (username, action, target) VALUES ($1, $2, $3)`,
		username, action, target,
	)
	return err
}

// GetUserPasswordHash คืน (hash, role) ของ username — ไม่พบคืน sql.ErrNoRows
func (s *Store) GetUserPasswordHash(username string) (string, string, error) {
	var hash, role string
	err := s.DB.QueryRow(
		`SELECT password_hash, role FROM users WHERE username = $1`, username,
	).Scan(&hash, &role)
	return hash, role, err
}

// CreateUser สร้างบัญชีผู้ใช้ใหม่ (passwordHash จัดเข้ารหัสจากผู้เรียก)
func (s *Store) CreateUser(u *models.User, passwordHash string) error {
	return s.DB.QueryRow(
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id, created_at`,
		u.Username, passwordHash, u.Role,
	).Scan(&u.ID, &u.CreatedAt)
}

// CustomerSummary ลูกค้าแบบย่อสำหรับหน้ารายการ พร้อมจำนวน service ที่ใช้อยู่
type CustomerSummary struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Company      string    `json:"company"`
	Contact      string    `json:"contact"`
	Email        string    `json:"email"`
	Phone        string    `json:"phone"`
	ServiceCount int       `json:"service_count"`
	CreatedAt    time.Time `json:"created_at"`
}

// ListCustomers รายการลูกค้า (ค้นหา q ใน name/email/phone) เรียงตามชื่อ
func (s *Store) ListCustomers(query string) ([]CustomerSummary, error) {
	where := ""
	args := []interface{}{}
	if q := strings.TrimSpace(query); q != "" {
		where = `WHERE c.name ILIKE $1 OR c.email ILIKE $1 OR c.phone ILIKE $1
			OR c.company ILIKE $1 OR c.contact ILIKE $1`
		args = append(args, "%"+q+"%")
	}
	rows, err := s.DB.Query(`
		SELECT c.id, c.name, c.company, c.contact, c.email, c.phone, c.created_at,
		       (SELECT COUNT(*) FROM services sv WHERE sv.customer_id = c.id)
		FROM customers c `+where+` ORDER BY c.name ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []CustomerSummary{}
	for rows.Next() {
		var c CustomerSummary
		if err := rows.Scan(&c.ID, &c.Name, &c.Company, &c.Contact, &c.Email, &c.Phone,
			&c.CreatedAt, &c.ServiceCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// itoa เล็กๆ สำหรับต่อ placeholder $n
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string([]byte{byte('0' + n/10), byte('0' + n%10)})
}
