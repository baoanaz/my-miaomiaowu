package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"miaomiaowu/internal/auth"
	"miaomiaowu/internal/logger"
	"miaomiaowu/internal/storage"
)

// 邀请码管理接口（管理员专用，本仓库增量功能）。
//
// 路由：
//   POST   /api/admin/invite-codes            生成（body: {"role":"admin"|"user"}）
//   GET    /api/admin/invite-codes            列表
//   DELETE /api/admin/invite-codes/{code}     吊销
//   GET    /api/admin/register-settings       读注册开关
//   PUT    /api/admin/register-settings       写注册开关（body: {"enabled":true}）

type inviteCodeDTO struct {
	Code      string  `json:"code"`
	Role      string  `json:"role"`
	CreatedBy string  `json:"created_by"`
	UsedBy    string  `json:"used_by"`
	UsedAt    *string `json:"used_at"`
	CreatedAt string  `json:"created_at"`
	Used      bool    `json:"used"`
}

type createInviteCodeRequest struct {
	Role string `json:"role"`
}

func toInviteCodeDTO(item storage.InviteCode) inviteCodeDTO {
	dto := inviteCodeDTO{
		Code:      item.Code,
		Role:      item.Role,
		CreatedBy: item.CreatedBy,
		UsedBy:    item.UsedBy,
		Used:      strings.TrimSpace(item.UsedBy) != "",
		CreatedAt: item.CreatedAt.Format(time.RFC3339),
	}
	if item.UsedAt != nil {
		s := item.UsedAt.Format(time.RFC3339)
		dto.UsedAt = &s
	}
	return dto
}

// NewInviteCodesHandler 邀请码的增删查。
func NewInviteCodesHandler(repo *storage.TrafficRepository) http.Handler {
	if repo == nil {
		panic("invite codes handler requires repository")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username := auth.UsernameFromContext(r.Context())
		if strings.TrimSpace(username) == "" {
			writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
			return
		}
		user, err := repo.GetUser(r.Context(), username)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if user.Role != storage.RoleAdmin {
			writeError(w, http.StatusForbidden, errors.New("only admin can manage invite codes"))
			return
		}

		// /api/admin/invite-codes 或 /api/admin/invite-codes/{code}
		path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/invite-codes"), "/")

		switch {
		case path == "" && r.Method == http.MethodGet:
			list, err := repo.ListInviteCodes(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			out := make([]inviteCodeDTO, 0, len(list))
			for _, item := range list {
				out = append(out, toInviteCodeDTO(item))
			}
			respondJSON(w, http.StatusOK, map[string]any{"invite_codes": out})

		case path == "" && r.Method == http.MethodPost:
			var payload createInviteCodeRequest
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				writeError(w, http.StatusBadRequest, errors.New("请求格式不正确"))
				return
			}
			role := strings.TrimSpace(payload.Role)
			if role == "" {
				role = storage.RoleUser
			}
			if role != storage.RoleAdmin && role != storage.RoleUser {
				writeError(w, http.StatusBadRequest, errors.New("role 只能是 admin 或 user"))
				return
			}

			item, err := repo.CreateInviteCode(r.Context(), username, role)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			logger.Info("[邀请码] 已生成", "code", item.Code, "role", role, "by", username)
			respondJSON(w, http.StatusCreated, map[string]any{"invite_code": toInviteCodeDTO(*item)})

		case path != "" && r.Method == http.MethodDelete:
			if err := repo.DeleteInviteCode(r.Context(), path); err != nil {
				if errors.Is(err, storage.ErrInviteCodeNotFound) {
					writeError(w, http.StatusNotFound, errors.New("邀请码不存在"))
					return
				}
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			logger.Info("[邀请码] 已吊销", "code", path, "by", username)
			respondJSON(w, http.StatusOK, map[string]any{"ok": true})

		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPost, http.MethodDelete)
		}
	})
}

// NewRegisterSettingsHandler 注册总开关（管理员）。
func NewRegisterSettingsHandler(repo *storage.TrafficRepository) http.Handler {
	if repo == nil {
		panic("register settings handler requires repository")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username := auth.UsernameFromContext(r.Context())
		if strings.TrimSpace(username) == "" {
			writeError(w, http.StatusUnauthorized, errors.New("unauthorized"))
			return
		}
		user, err := repo.GetUser(r.Context(), username)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if user.Role != storage.RoleAdmin {
			writeError(w, http.StatusForbidden, errors.New("only admin can change register settings"))
			return
		}

		switch r.Method {
		case http.MethodGet:
			respondJSON(w, http.StatusOK, map[string]any{
				"enabled": isRegisterEnabled(r.Context(), repo),
			})

		case http.MethodPut:
			var payload struct {
				Enabled bool `json:"enabled"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				writeError(w, http.StatusBadRequest, errors.New("请求格式不正确"))
				return
			}
			value := "0"
			if payload.Enabled {
				value = "1"
			}
			if err := repo.SetSystemSetting(r.Context(), settingRegisterEnabled, value); err != nil {
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			logger.Info("[注册] 开关已变更", "enabled", payload.Enabled, "by", username)
			respondJSON(w, http.StatusOK, map[string]any{"enabled": payload.Enabled})

		default:
			methodNotAllowed(w, http.MethodGet, http.MethodPut)
		}
	})
}
