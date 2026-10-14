// Package agent นิยามสัญญา (interface) ของ "เอเจนต์" แต่ละตัวในระบบ
// เอเจนต์ทุกตัวต้องรันได้เป็นอิสระ ตรวจสุขภาพได้ และหยุดตาม context ได้
package agent

import "context"

// Agent คือหน่วยงานย่อย 1 ตัวที่ manager ดูแล
type Agent interface {
	// Name คืนชื่อเอเจนต์ (ใช้ใน log และ /healthz)
	Name() string

	// Run ทำงานจนกว่า ctx จะถูกยกเลิก
	// คืน nil เมื่อถูกสั่งหยุดตามปกติ, คืน error เมื่อผิดพลาดร้ายแรง
	// (manager จะรีสตาร์ทให้ทั้งสองกรณีถ้าระบบยังไม่ปิด)
	Run(ctx context.Context) error

	// Health ตรวจว่าเอเจนต์ยังใช้งานได้จริงหรือไม่ คืน nil = ปกติ
	// manager เรียกเป็นระยะ ถ้าล้มเหลวติดกันหลายครั้งจะสั่งรีสตาร์ท
	Health(ctx context.Context) error
}
