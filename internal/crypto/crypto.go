// Package crypto เข้ารหัส/ถอดรหัสรหัสผ่านลูกค้าด้วย AES-256-GCM
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

var gcm cipher.AEAD // ตัวเข้ารหัส ตั้งค่าครั้งเดียวตอน Init

// Init ตั้ง key (32 bytes จาก config) — เรียกครั้งเดียวตอนเริ่มโปรแกรม
func Init(key string) error {
	if len(key) != 32 {
		return fmt.Errorf("key ต้องยาว 32 bytes (ปัจจุบัน %d)", len(key))
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return err
	}
	gcm, err = cipher.NewGCM(block)
	if err != nil {
		return err
	}
	return nil
}

// Encrypt เข้ารหัส AES-256-GCM → คืน base64 std encoding พร้อม random nonce ต่อท้าย
func Encrypt(plaintext string) (string, error) {
	if gcm == nil {
		return "", errors.New("crypto ยังไม่ได้ Init")
	}
	nonce := make([]byte, gcm.NonceSize()) // 12 bytes
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, []byte(plaintext), nil) // nonce || ciphertext
	return base64.StdEncoding.EncodeToString(out), nil
}

// Decrypt ถอดรหัสที่ได้จาก Encrypt
func Decrypt(ciphertext string) (string, error) {
	if gcm == nil {
		return "", errors.New("crypto ยังไม่ได้ Init")
	}
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", errors.New("ข้อมูลสั้นเกินไป")
	}
	nonce, enc := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, enc, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
