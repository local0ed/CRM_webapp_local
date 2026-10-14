# CRM (Go Multi-Agent)

ระบบ CRM สำหรับติดตามลูกค้าและบริการ เขียนด้วย Go ทำงานแบบ **multi-agent**
โดยมี supervisor (manager) คอยรันและเฝ้าดูเอเจนต์ย่อยหลายตัว

## สถาปัตยกรรม

- `main.go` — manager/supervisor: รันเอเจนต์เป็น goroutine, health check ทุก 15s,
  ล้ม 2 ครั้งติดกันแล้วรีสตาร์ท (backoff 2s→30s), ปิดอย่างสง่างามด้วย signal
- เอเจนต์ 4 ตัว (`internal/agents/`):
  - `db` — ping + migrate + heal ฐานข้อมูล
  - `notify` — แจ้งเตือนอีเมลทุก 24 ชั่วโมง
  - `http` — API server บนพอร์ต `:8080`
  - `journal` — บันทึกกิจกรรมลง `data/journal.jsonl` (อ่านกลับได้เมื่อบูตใหม่)
- สัญญาเอเจนต์: `internal/agent.Agent` (Name / Run / Health)

## ฟีเจอร์

- ระบบ login / session (cookie)
- CRUD ลูกค้า (พร้อม `company`, `contact`, `address`, `tax_id`)
- บริการ / โปรแกรมที่ลูกค้าซื้อ — ติดตามเอกสารอ้างอิง (invoice/quotation),
  สถานะการเงิน (ราคา / ยอดชำระ), สถานะใบเสนอราคา, การต่ออายุ
- ระบบรายงาน — สรุปการใช้งาน, ตารางบริการใกล้หมดอายุ, สรุปการเงิน, ดาวน์โหลด CSV
- Web UI (SPA ไฟล์เดียว) ที่ `web/index.html` — รองรับภาษาไทย + สลับธีม

## การรัน

ต้องมี PostgreSQL ที่ `localhost:5432/crm_demo` (ระบบรอ DB ได้ ไม่ตายตอนบูต)

```sh
go run .
# หรือ
go build -o crm . && ./crm
```

บัญชีเดโม: `admin` / `admin123`

## โครงสร้างไดเรกทอรี

```
main.go
web/index.html
internal/
  agent/     # สัญญา Agent
  agents/    # เอเจนต์ db, notify, http, journal
  manager/   # supervisor
  api/       # HTTP API
  repo/      # data access
  models/
  db/        # connection + migration
  auth/      # login / session
  config/
  mail/      # mailer
  crypto/
```

## เทคโนโลยี

- Go 1.27
- `github.com/lib/pq` (PostgreSQL driver)
- `golang.org/x/crypto`
