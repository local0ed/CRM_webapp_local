// Package api HTTP API ของระบบ CRM (JSON) พร้อม session, rate limit และ /healthz
package api

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"crm/internal/agents"
	"crm/internal/auth"
	"crm/internal/crypto"
	"crm/internal/manager"
	"crm/internal/models"
	"crm/internal/repo"
)

const sessionCookie = "crm_session"

// API รวม dependencies ที่ handler ต้องใช้
type API struct {
	store   *repo.Store
	sess    *auth.Sessions
	rl      *auth.RateLimiter
	db      *sql.DB
	mgr     *manager.Manager
	journal *agents.JournalAgent
	logger  *log.Logger
}

// New ประกอบ router ทั้งหมด
func New(store *repo.Store, sess *auth.Sessions, rl *auth.RateLimiter,
	db *sql.DB, mgr *manager.Manager, journal *agents.JournalAgent, logger *log.Logger) http.Handler {

	a := &API{store: store, sess: sess, rl: rl, db: db, mgr: mgr, journal: journal, logger: logger}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.healthz)

	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("POST /api/logout", a.auth(a.logout))
	mux.HandleFunc("GET /api/me", a.auth(a.me))

	mux.HandleFunc("GET /api/services", a.auth(a.listServices))
	mux.HandleFunc("GET /api/services/{id}", a.auth(a.serviceDetail))
	mux.HandleFunc("PUT /api/services/{id}", a.admin(a.updateService))
	mux.HandleFunc("GET /api/services/{id}/password", a.auth(a.servicePassword))

	mux.HandleFunc("GET /api/customers", a.auth(a.listCustomers))
	mux.HandleFunc("GET /api/customers/{id}", a.auth(a.getCustomer))
	mux.HandleFunc("POST /api/customers", a.admin(a.createCustomer))
	mux.HandleFunc("PUT /api/customers/{id}", a.admin(a.updateCustomer))
	mux.HandleFunc("DELETE /api/customers/{id}", a.admin(a.deleteCustomer))

	mux.HandleFunc("GET /api/expiring", a.auth(a.expiring))
	mux.HandleFunc("GET /api/reports/summary", a.auth(a.reportSummary))
	mux.HandleFunc("GET /api/reports/expiring", a.auth(a.reportExpiring))
	mux.HandleFunc("GET /api/notifications", a.auth(a.listNotifications))
	mux.HandleFunc("GET /api/journal", a.auth(a.listJournal))

	// ไฟล์ static จากโฟลเดอร์ web/ ("/" → index.html) — /api/* และ /healthz ไม่ตกมาที่นี่
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
			fail(w, http.StatusNotFound, "ไม่พบ endpoint นี้")
			return
		}
		http.ServeFile(w, r, filepath.Join("web", filepath.Clean("/"+r.URL.Path)))
	})

	return mux
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("id ไม่ถูกต้อง")
	}
	return id, nil
}

type ctxKey int

const sessionKey ctxKey = 1

// auth middleware: ตรวจ session จาก cookie แล้วส่ง SessionInfo ต่อใน context
func (a *API) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			fail(w, http.StatusUnauthorized, "ยังไม่ได้เข้าสู่ระบบ")
			return
		}
		info, ok := a.sess.Get(c.Value)
		if !ok {
			fail(w, http.StatusUnauthorized, "session หมดอายุ กรุณาเข้าสู่ระบบใหม่")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), sessionKey, info)))
	}
}

// admin middleware: เหมือน auth แต่ต้องเป็น role admin (สำหรับคำสั่งเขียนข้อมูล)
func (a *API) admin(next http.HandlerFunc) http.HandlerFunc {
	return a.auth(func(w http.ResponseWriter, r *http.Request) {
		info := r.Context().Value(sessionKey).(*auth.SessionInfo)
		if info.Role != "admin" && subtle.ConstantTimeCompare([]byte(info.Role), []byte("admin")) != 1 {
			fail(w, http.StatusForbidden, "ต้องเป็น admin เท่านั้น")
			return
		}
		next(w, r)
	})
}

// ---- พื้นฐาน ----

func (a *API) root(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!doctype html><html lang="th"><meta charset="utf-8">
<title>CRM API</title>
<body style="font-family:sans-serif;max-width:40rem;margin:3rem auto;line-height:1.7">
<h1>CRM ระบบ multi-agent</h1>
<p>เซิร์ฟเวอร์ทำงานอยู่ ดูสถานะเอเจนต์ทุกตัวที่ <a href="/healthz">/healthz</a></p>
<p>API: POST /api/login · GET /api/services · GET /api/customers/{id} · GET /api/journal</p>
</body></html>`))
}

// healthz รายงานสถานะเอเจนต์ทุกตัว + ผล ping ฐานข้อมูล
func (a *API) healthz(w http.ResponseWriter, r *http.Request) {
	dbOK := a.db.PingContext(r.Context()) == nil
	state := "ok"
	for _, s := range a.mgr.Status() {
		if s.State != manager.StateRunning {
			state = "degraded"
		}
	}
	if !dbOK {
		state = "degraded"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   state,
		"database": dbOK,
		"agents":   a.mgr.Status(),
	})
}

// ---- session ----

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	// จำกัด 5 ครั้ง/นาที/IP กันเดารหัสผ่าน
	if !a.rl.Allow("login:" + r.RemoteAddr) {
		fail(w, http.StatusTooManyRequests, "ลองหลายครั้งเกินไป รอสักครู่แล้วลองใหม่")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "รูปแบบข้อมูลไม่ถูกต้อง")
		return
	}

	hash, role, err := a.store.GetUserPasswordHash(body.Username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusUnauthorized, "ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง")
			return
		}
		fail(w, http.StatusServiceUnavailable, "ฐานข้อมูลยังไม่พร้อม ลองใหม่อีกครั้ง")
		return
	}
	if !auth.VerifyPassword(hash, body.Password) {
		a.journal.Append("api", "เข้าสู่ระบบล้มเหลว: "+body.Username)
		fail(w, http.StatusUnauthorized, "ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง")
		return
	}

	token := a.sess.New(body.Username, role)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   12 * 3600,
	})
	a.journal.Append("api", "เข้าสู่ระบบ: "+body.Username)
	a.store.RecordAudit(body.Username, "login", body.Username)
	writeJSON(w, http.StatusOK, map[string]string{"username": body.Username, "role": role})
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.sess.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]string{"ok": "ออกจากระบบแล้ว"})
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	info := r.Context().Value(sessionKey).(*auth.SessionInfo)
	writeJSON(w, http.StatusOK, info)
}

// ---- services ----

func (a *API) listServices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize < 1 {
		pageSize = 20
	}
	items, total, err := a.store.ListServices(q.Get("query"), q.Get("status"), page, pageSize)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "page": page, "page_size": pageSize,
	})
}

func (a *API) servicePassword(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	enc, err := a.store.GetEncryptedPassword(id)
	if err != nil {
		fail(w, http.StatusNotFound, "ไม่พบ service นี้")
		return
	}
	pw, err := crypto.Decrypt(enc)
	if err != nil {
		fail(w, http.StatusInternalServerError, "ถอดรหัสไม่สำเร็จ (key ไม่ตรงกับตอนเข้ารหัส?)")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "password": pw})
}

// serviceDetail คืนรายละเอียด service รายตัว (พร้อม customer_name)
func (a *API) serviceDetail(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	v, err := a.store.GetService(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "ไม่พบ service นี้")
		} else {
			fail(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// updateService แก้ไข service รายตัว (เปิดจากหน้า detail) — รหัสผ่านว่าง = คงเดิม
func (a *API) updateService(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	var in serviceIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "รูปแบบข้อมูลไม่ถูกต้อง")
		return
	}
	sv := models.Service{
		ProgramName:    in.ProgramName,
		Domain:         in.Domain,
		Username:       in.Username,
		RegisteredFrom: orZero(in.RegisteredFrom.t),
		RegisteredTo:   orZero(in.RegisteredTo.t),
		RefInvoice:     in.RefInvoice,
		RefQuotation:   in.RefQuotation,
		Price:          in.Price,
		AmountPaid:     in.AmountPaid,
		PaidDate:       orNil(in.PaidDate.t),
		QuoteStatus:    in.QuoteStatus,
		QuoteDate:      orNil(in.QuoteDate.t),
		RenewStatus:    in.RenewStatus,
		Notes:          in.Notes,
	}
	if in.Password != "" {
		enc, err := crypto.Encrypt(in.Password)
		if err != nil {
			fail(w, http.StatusInternalServerError, "เข้ารหัสรหัสผ่านไม่สำเร็จ")
			return
		}
		sv.PasswordEnc = enc
	}
	if err := a.store.UpdateService(id, &sv); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "ไม่พบ service นี้")
		} else {
			fail(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	info := r.Context().Value(sessionKey).(*auth.SessionInfo)
	a.journal.Append("api", info.Username+" แก้ไข service id="+strconv.FormatInt(id, 10))
	a.store.RecordAudit(info.Username, "update_service", strconv.FormatInt(id, 10))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- customers ----

// serviceIn ข้อมูล service จากฝั่งผู้ใช้ — รหัสผ่านเป็นข้อความล้วน จะเข้ารหัสก่อนเก็บ
type serviceIn struct {
	ID             int64      `json:"id"`
	ProgramName    string     `json:"program_name"`
	Domain         string     `json:"domain"`
	Username       string     `json:"username"`
	Password       string     `json:"password"` // ว่าง = คงรหัสเดิมไว้ (ตอนแก้ไข)
	RegisteredFrom dateOrTime `json:"registered_from"`
	RegisteredTo   dateOrTime `json:"registered_to"`
	RefInvoice     string     `json:"ref_invoice"`
	RefQuotation   string     `json:"ref_quotation"`
	Price          float64    `json:"price"`
	AmountPaid     float64    `json:"amount_paid"`
	PaidDate       dateOrTime `json:"paid_date"`
	QuoteStatus    string     `json:"quote_status"` // none | sent | approved
	QuoteDate      dateOrTime `json:"quote_date"`
	RenewStatus    string     `json:"renew_status"` // none | renewed
	Notes          string     `json:"notes"`
}

// dateOrTime ยอมรับวันที่ได้ทั้งสองแบบ: "YYYY-MM-DD" (จาก <input type="date">)
// และ RFC3339 (มาตรฐาน time.Time) — ป้องกันฟอร์มหน้าเว็บส่งวันที่เปลือยแล้ว decode ล้มเหลว
type dateOrTime struct {
	t *time.Time
}

func (d *dateOrTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		d.t = nil
		return nil
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			d.t = &t
			return nil
		}
	}
	return fmt.Errorf("รูปแบบวันที่ไม่ถูกต้อง: %s", s)
}

type customerIn struct {
	Name     string      `json:"name"`
	Company  string      `json:"company"`
	Contact  string      `json:"contact"`
	Email    string      `json:"email"`
	Phone    string      `json:"phone"`
	Address  string      `json:"address"`
	TaxID    string      `json:"tax_id"`
	Note     string      `json:"note"`
	Services []serviceIn `json:"services"`
}

// toModels แปลง DTO → models พร้อมเข้ารหัสรหัสผ่านที่ส่งมา
func (ci *customerIn) toModels() (*models.Customer, []models.Service, error) {
	c := &models.Customer{Name: ci.Name, Company: ci.Company, Contact: ci.Contact,
		Email: ci.Email, Phone: ci.Phone, Address: ci.Address, TaxID: ci.TaxID, Note: ci.Note}
	svcs := make([]models.Service, 0, len(ci.Services))
	for _, s := range ci.Services {
		sv := models.Service{
			ID:             s.ID,
			ProgramName:    s.ProgramName,
			Domain:         s.Domain,
			Username:       s.Username,
			RegisteredFrom: orZero(s.RegisteredFrom.t),
			RegisteredTo:   orZero(s.RegisteredTo.t),
			RefInvoice:     s.RefInvoice,
			RefQuotation:   s.RefQuotation,
			Price:          s.Price,
			AmountPaid:     s.AmountPaid,
			PaidDate:       orNil(s.PaidDate.t),
			QuoteStatus:    s.QuoteStatus,
			QuoteDate:      orNil(s.QuoteDate.t),
			RenewStatus:    s.RenewStatus,
			Notes:          s.Notes,
		}
		if s.Password != "" {
			enc, err := crypto.Encrypt(s.Password)
			if err != nil {
				return nil, nil, err
			}
			sv.PasswordEnc = enc
		}
		svcs = append(svcs, sv)
	}
	return c, svcs, nil
}

func orZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// orNil คืน pointer เดิม (nil ได้) — สำหรับ field วันที่ที่ยอมให้เป็น null
func orNil(t *time.Time) *time.Time {
	return t
}

func (a *API) createCustomer(w http.ResponseWriter, r *http.Request) {
	var in customerIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "รูปแบบข้อมูลไม่ถูกต้อง")
		return
	}
	c, svcs, err := in.toModels()
	if err != nil {
		fail(w, http.StatusInternalServerError, "เข้ารหัสรหัสผ่านไม่สำเร็จ")
		return
	}
	id, err := a.store.CreateCustomerWithServices(c, svcs)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	info := r.Context().Value(sessionKey).(*auth.SessionInfo)
	a.journal.Append("api", info.Username+" สร้างลูกค้า: "+c.Name)
	a.store.RecordAudit(info.Username, "create_customer", c.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (a *API) updateCustomer(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	var in customerIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "รูปแบบข้อมูลไม่ถูกต้อง")
		return
	}
	c, svcs, err := in.toModels()
	if err != nil {
		fail(w, http.StatusInternalServerError, "เข้ารหัสรหัสผ่านไม่สำเร็จ")
		return
	}
	c.ID = id
	if err := a.store.UpdateCustomer(c, svcs); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	info := r.Context().Value(sessionKey).(*auth.SessionInfo)
	a.journal.Append("api", info.Username+" แก้ไขลูกค้า id="+strconv.FormatInt(id, 10))
	a.store.RecordAudit(info.Username, "update_customer", strconv.FormatInt(id, 10))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) deleteCustomer(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.store.DeleteCustomer(id); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	info := r.Context().Value(sessionKey).(*auth.SessionInfo)
	a.journal.Append("api", info.Username+" ลบลูกค้า id="+strconv.FormatInt(id, 10))
	a.store.RecordAudit(info.Username, "delete_customer", strconv.FormatInt(id, 10))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) listCustomers(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListCustomers(r.URL.Query().Get("query"))
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *API) getCustomer(w http.ResponseWriter, r *http.Request) {	id, err := pathID(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	c, svcs, err := a.store.GetCustomerWithServices(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "ไม่พบลูกค้านี้")
		} else {
			fail(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"customer": c, "services": svcs})
}

// ---- รายงาน/บันทึก ----

// reportSummary คืนข้อมูลสรุปสำหรับหน้ารายงาน (นับ + สรุปตามโปรแกรม)
func (a *API) reportSummary(w http.ResponseWriter, r *http.Request) {
	rp, err := a.store.GetReportSummary()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rp)
}

// reportExpiring คืนรายการ service ที่จะหมดอายุภายใน days วัน (ค่าเริ่มต้น 30)
func (a *API) reportExpiring(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days < 1 {
		days = 30
	}
	items, err := a.store.ExpiringWithin(days)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days, "items": items, "total": len(items)})
}

func (a *API) expiring(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	items, err := a.store.GetExpiring(days)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days, "items": items})
}

func (a *API) listNotifications(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := a.store.ListNotifications(limit)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *API) listJournal(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	writeJSON(w, http.StatusOK, map[string]any{"items": a.journal.Last(limit)})
}
