package crypto

import (
	"strings"
	"testing"
)

func TestRoundtrip(t *testing.T) {
	if err := Init("0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	enc, err := Encrypt("สวัสดี secret-123")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(enc, "secret") {
		t.Fatal("ciphertext ต้องไม่มี plaintext อยู่ในนั้นตรงๆ")
	}
	dec, err := Decrypt(enc)
	if err != nil {
		t.Fatal(err)
	}
	if dec != "สวัสดี secret-123" {
		t.Fatalf("ได้ %q ที่ต้องเป็น %q", dec, "สวัสดี secret-123")
	}
}

func TestWrongKeyFails(t *testing.T) {
	if err := Init("0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	enc, err := Encrypt("ข้อมูลลับ")
	if err != nil {
		t.Fatal(err)
	}
	// เปลี่ยน key เป็นคนละตัว ถอดรหัสต้อง fail
	if err := Init("ffffffffffffffffffffffffffffffff"); err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(enc); err == nil {
		t.Fatal("ถอดรหัสด้วย key ผิด ต้อง error")
	}
}

func TestNonceUnique(t *testing.T) {
	if err := Init("0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	a, _ := Encrypt("เดียวกัน")
	b, _ := Encrypt("เดียวกัน")
	if a == b {
		t.Fatal("nonce ต้องสุ่มใหม่ทุกครั้ง ciphertext ไม่ควรเหมือนกัน")
	}
}

func TestNotInit(t *testing.T) {
	// reset ให้ยังไม่ Init
	gcm = nil
	if _, err := Encrypt("x"); err == nil {
		t.Fatal("Encrypt ก่อน Init ต้อง error")
	}
	if _, err := Decrypt("x"); err == nil {
		t.Fatal("Decrypt ก่อน Init ต้อง error")
	}
}

func TestBadKey(t *testing.T) {
	if err := Init("สั้นเกินไป"); err == nil {
		t.Fatal("key ไม่ครบ 32 bytes ต้อง error")
	}
}
