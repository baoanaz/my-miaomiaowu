package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 邀请码存储层（本仓库对上游的增量功能）。
//
// 设计上刻意放在独立文件、独立表里，不碰上游既有的 users / system_settings 结构：
// 上游更新时这里几乎不会产生 merge 冲突，最坏情况只是 migrate 里多一行调用。
//
// 表结构：
//
//	code        邀请码本体，形如 A7K2M9：首位决定注册出来的角色
//	              A = 管理员（admin）, B = 普通用户（user）
//	role        冗余保存角色，便于查询时不必再解析首位
//	created_by  生成者（管理员用户名）
//	used_by     使用者用户名；为空表示未使用
//	used_at     使用时间
//	created_at  生成时间
//
// 一次性语义：used_by 非空即视为已失效，不做"可重复使用"的开关，
// 因为可重复的管理员码等同于永久后门。

// InviteCode 邀请码实体。
type InviteCode struct {
	Code      string
	Role      string
	CreatedBy string
	UsedBy    string
	UsedAt    *time.Time
	CreatedAt time.Time
}

var (
	// ErrInviteCodeNotFound 邀请码不存在。
	ErrInviteCodeNotFound = errors.New("invite code not found")
	// ErrInviteCodeUsed 邀请码已被使用。
	ErrInviteCodeUsed = errors.New("invite code already used")
)

const inviteCodeSchema = `
CREATE TABLE IF NOT EXISTS invite_codes (
    code TEXT PRIMARY KEY,
    role TEXT NOT NULL DEFAULT 'user',
    created_by TEXT NOT NULL DEFAULT '',
    used_by TEXT NOT NULL DEFAULT '',
    used_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_invite_codes_used_by ON invite_codes(used_by);
`

// migrateInviteCodes 建表。由 TrafficRepository.migrate() 调用。
func (r *TrafficRepository) migrateInviteCodes() error {
	if _, err := r.db.Exec(inviteCodeSchema); err != nil {
		return fmt.Errorf("migrate invite_codes: %w", err)
	}
	return nil
}

// inviteCodeAlphabet 去掉了容易看混的 I/O/0/1，邀请码要口头或手抄给朋友。
const inviteCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GenerateInviteCode 生成邀请码：首字母 A（管理员）或 B（普通用户）+ 5 位随机字符。
func GenerateInviteCode(role string) (string, error) {
	prefix := "B"
	if role == RoleAdmin {
		prefix = "A"
	}

	buf := make([]byte, 5)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate invite code: %w", err)
	}
	var sb strings.Builder
	sb.WriteString(prefix)
	n := byte(len(inviteCodeAlphabet))
	for _, b := range buf {
		sb.WriteByte(inviteCodeAlphabet[int(b)%int(n)])
	}
	return sb.String(), nil
}

// RoleFromInviteCode 从邀请码首位推断角色；非法则返回空串。
// 注意：实际注册时以数据库里存的 role 为准，这里只用于生成阶段的校验。
func RoleFromInviteCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return ""
	}
	switch code[0] {
	case 'A':
		return RoleAdmin
	case 'B':
		return RoleUser
	default:
		return ""
	}
}

// CreateInviteCode 生成并落库。role 只接受 admin / user。
func (r *TrafficRepository) CreateInviteCode(ctx context.Context, createdBy, role string) (*InviteCode, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("traffic repository not initialized")
	}
	if role != RoleAdmin && role != RoleUser {
		return nil, errors.New("invalid role")
	}

	// 极小概率撞码：重试若干次
	for attempt := 0; attempt < 8; attempt++ {
		code, err := GenerateInviteCode(role)
		if err != nil {
			return nil, err
		}
		_, err = r.db.ExecContext(ctx,
			`INSERT INTO invite_codes (code, role, created_by) VALUES (?, ?, ?)`,
			code, role, createdBy)
		if err == nil {
			return &InviteCode{Code: code, Role: role, CreatedBy: createdBy, CreatedAt: time.Now()}, nil
		}
		// 撞码时重试；其他错误直接返回（与上游 CreateUser 同样用字符串匹配判断唯一冲突）
		if !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, fmt.Errorf("create invite code: %w", err)
		}
	}
	return nil, errors.New("failed to generate a unique invite code")
}

// GetInviteCode 查询单个邀请码。
func (r *TrafficRepository) GetInviteCode(ctx context.Context, code string) (*InviteCode, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("traffic repository not initialized")
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return nil, ErrInviteCodeNotFound
	}

	var item InviteCode
	var usedAt *time.Time
	err := r.db.QueryRowContext(ctx,
		`SELECT code, role, created_by, used_by, used_at, created_at
		 FROM invite_codes WHERE code = ?`, code).
		Scan(&item.Code, &item.Role, &item.CreatedBy, &item.UsedBy, &usedAt, &item.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInviteCodeNotFound
		}
		return nil, fmt.Errorf("get invite code: %w", err)
	}
	item.UsedAt = usedAt
	return &item, nil
}

// ConsumeInviteCode 校验并标记为已使用。
// 用条件更新（used_by = ''）保证并发下同一个码只会被消费一次。
func (r *TrafficRepository) ConsumeInviteCode(ctx context.Context, code, username string) (*InviteCode, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("traffic repository not initialized")
	}
	code = strings.ToUpper(strings.TrimSpace(code))

	item, err := r.GetInviteCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(item.UsedBy) != "" {
		return nil, ErrInviteCodeUsed
	}

	res, err := r.db.ExecContext(ctx,
		`UPDATE invite_codes SET used_by = ?, used_at = CURRENT_TIMESTAMP
		 WHERE code = ? AND used_by = ''`, username, code)
	if err != nil {
		return nil, fmt.Errorf("consume invite code: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("consume invite code: %w", err)
	}
	if affected == 0 {
		// 竞态：别人刚用掉了
		return nil, ErrInviteCodeUsed
	}
	return item, nil
}

// ListInviteCodes 列出所有邀请码（新→旧）。
func (r *TrafficRepository) ListInviteCodes(ctx context.Context) ([]InviteCode, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("traffic repository not initialized")
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT code, role, created_by, used_by, used_at, created_at
		 FROM invite_codes ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list invite codes: %w", err)
	}
	defer rows.Close()

	var items []InviteCode
	for rows.Next() {
		var item InviteCode
		var usedAt *time.Time
		if err := rows.Scan(&item.Code, &item.Role, &item.CreatedBy, &item.UsedBy,
			&usedAt, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan invite code: %w", err)
		}
		item.UsedAt = usedAt
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate invite codes: %w", err)
	}
	return items, nil
}

// DeleteInviteCode 吊销邀请码（未使用的也可以撤销）。
func (r *TrafficRepository) DeleteInviteCode(ctx context.Context, code string) error {
	if r == nil || r.db == nil {
		return errors.New("traffic repository not initialized")
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return ErrInviteCodeNotFound
	}

	res, err := r.db.ExecContext(ctx, `DELETE FROM invite_codes WHERE code = ?`, code)
	if err != nil {
		return fmt.Errorf("delete invite code: %w", err)
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return ErrInviteCodeNotFound
	}
	return nil
}
