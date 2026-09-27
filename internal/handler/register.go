package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"miaomiaowu/internal/captcha"
	"miaomiaowu/internal/logger"
	"miaomiaowu/internal/storage"
)

// 用户自助注册 + 邀请码（本仓库对上游的增量功能）。
//
// 流程：管理员在「用户管理」生成邀请码 → 朋友拿码在 /register 注册 → 自动建号并授权订阅。
// 邀请码首位决定角色：A = 管理员，B = 普通用户；一次性，用过即失效。
//
// 设计要点：
//  1. 不开上游的表、不改上游结构体：邀请码独占 invite_codes 表，
//     开关与订阅映射走 system_settings 键值对，迁移时 merge 冲突面极小。
//  2. 复用登录同款的 Turnstile 验证码与限流器，注册接口同样受防爆破保护。
//  3. 管理员码（A）风险高：前端弹窗会显著警告，且强制一次性。
//
// 相关 system_settings 键：
//
//	register_enabled   "1"/"0"  是否开放注册（默认关闭，需管理员显式开启）
const settingRegisterEnabled = "register_enabled"

// 用户名白名单：只允许字母、数字、下划线、连字符，长度 3-32。
// 放开中文等字符会让订阅链接里的 username 变成 URL 编码，得不偿失。
var registerUsernameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)

type registerRequest struct {
	Username       string `json:"username"`
	Password       string `json:"password"`
	Email          string `json:"email"`
	InviteCode     string `json:"invite_code"`
	TurnstileToken string `json:"turnstile_token"`
}

type registerResponse struct {
	Username string `json:"username"`
	Message  string `json:"message"`
}

// NewRegisterStatusHandler 公开端点：注册页用它决定是否显示表单。
// 只暴露"是否开放"与验证码配置，不泄漏任何邀请码相关信息。
func NewRegisterStatusHandler(repo *storage.TrafficRepository, verifier *captcha.Turnstile) http.Handler {
	if repo == nil {
		panic("register status handler requires repository")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}

		payload := map[string]any{
			"enabled":         isRegisterEnabled(r.Context(), repo),
			"invite_required": true, // 本实现始终需要邀请码
			"captcha_enabled": verifier != nil && verifier.Enabled(r.Context()),
		}
		if verifier != nil {
			payload["captcha_site_key"] = verifier.SiteKey(r.Context())
		}
		respondJSON(w, http.StatusOK, payload)
	})
}

// NewRegisterHandler 注册接口：凭邀请码建号。
func NewRegisterHandler(
	repo *storage.TrafficRepository,
	rateLimiter *LoginRateLimiter,
	verifier *captcha.Turnstile,
) http.Handler {
	if repo == nil {
		panic("register handler requires repository")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}

		if !isRegisterEnabled(r.Context(), repo) {
			writeError(w, http.StatusForbidden, errors.New("注册功能未开放"))
			return
		}

		var payload registerRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, errors.New("请求格式不正确"))
			return
		}

		username := strings.TrimSpace(payload.Username)
		password := strings.TrimSpace(payload.Password)
		email := strings.TrimSpace(payload.Email)
		inviteCode := strings.ToUpper(strings.TrimSpace(payload.InviteCode))
		clientIP := GetClientIP(r)

		// 验证码（未配置 Turnstile 时 Verify 直接通过）
		if verifier != nil && !verifier.Verify(r.Context(), payload.TurnstileToken, clientIP) {
			writeError(w, http.StatusBadRequest, errors.New("人机验证失败"))
			return
		}

		// 复用登录限流：同一 IP 短时间大量尝试会被拦
		if rateLimiter != nil {
			if err := rateLimiter.Check(clientIP, username); err != nil {
				writeError(w, http.StatusTooManyRequests, errors.New("尝试过于频繁，请稍后再试"))
				return
			}
		}

		if !registerUsernameRE.MatchString(username) {
			writeError(w, http.StatusBadRequest,
				errors.New("用户名只能包含字母、数字、下划线和连字符，长度 3-32 位"))
			return
		}
		if len(password) < 8 {
			writeError(w, http.StatusBadRequest, errors.New("密码至少 8 位"))
			return
		}
		if email != "" && !strings.Contains(email, "@") {
			writeError(w, http.StatusBadRequest, errors.New("邮箱格式不正确"))
			return
		}
		if inviteCode == "" {
			writeError(w, http.StatusBadRequest, errors.New("请填写邀请码"))
			return
		}

		// 先查邀请码拿角色（此时还不消费，避免建号失败却把码用掉）
		invite, err := repo.GetInviteCode(r.Context(), inviteCode)
		if err != nil {
			if errors.Is(err, storage.ErrInviteCodeNotFound) {
				if rateLimiter != nil {
					rateLimiter.RecordFailure(clientIP, username)
				}
				writeError(w, http.StatusForbidden, errors.New("邀请码无效"))
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if strings.TrimSpace(invite.UsedBy) != "" {
			writeError(w, http.StatusForbidden, errors.New("邀请码已被使用"))
			return
		}

		role := invite.Role
		if role != storage.RoleAdmin && role != storage.RoleUser {
			role = storage.RoleUser // 脏数据兜底，绝不因数据问题误发管理员
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}

		// nickname 缺省等于 username
		if err := repo.CreateUser(r.Context(), username, email, username, string(hash), role, ""); err != nil {
			if errors.Is(err, storage.ErrUserExists) {
				if rateLimiter != nil {
					rateLimiter.RecordFailure(clientIP, username)
				}
				writeError(w, http.StatusConflict, errors.New("用户名已被占用"))
				return
			}
			logger.Error("[注册] 创建用户失败", "username", username, "error", err)
			writeError(w, http.StatusInternalServerError, errors.New("注册失败，请稍后重试"))
			return
		}

		// 建号成功后消费邀请码。条件更新保证并发下只被消费一次；
		// 若这里失败（极少见），说明码被别人同时用掉了 —— 回滚刚建的用户，避免绕过一次性限制。
		if _, err := repo.ConsumeInviteCode(r.Context(), inviteCode, username); err != nil {
			_ = repo.DeleteUser(r.Context(), username)
			logger.Info("[注册] 邀请码消费失败，已回滚用户", "username", username, "error", err)
			writeError(w, http.StatusConflict, errors.New("邀请码已被使用"))
			return
		}

		// 授权订阅：管理员看得到全部订阅（无需显式授权），普通用户授权「普通用户配置」
		if role == storage.RoleUser {
			if err := grantGuestSubscription(r.Context(), repo, username); err != nil {
				// 不回滚用户：账号已建好，管理员可在面板补授权
				logger.Info("[注册] 自动授权订阅失败，可在面板手动补", "username", username, "error", err)
			}
		}

		if rateLimiter != nil {
			rateLimiter.RecordSuccess(clientIP, username)
		}

		logger.Info("[注册] 新用户注册成功", "username", username, "role", role, "ip", clientIP)
		respondJSON(w, http.StatusCreated, registerResponse{
			Username: username,
			Message:  "注册成功，请返回登录页登录",
		})
	})
}

// isRegisterEnabled 读取开关。键不存在或写坏都视为"关闭"——注册是敏感功能，
// 默认必须是关的，避免升级后意外对外开放。
func isRegisterEnabled(ctx context.Context, repo *storage.TrafficRepository) bool {
	v, err := repo.GetSystemSetting(ctx, settingRegisterEnabled)
	if err != nil {
		return false
	}
	return strings.TrimSpace(v) == "1"
}

// guestSubscriptionFilename 普通用户注册后自动获得的订阅文件名。
// 与 mmw_setup.py 里创建的「普通用户配置」保持一致。
const guestSubscriptionFilename = "mmw-guest.yaml"

// grantGuestSubscription 给新普通用户授权「普通用户配置」。
// 找不到该订阅时只记日志，不让注册整体失败（管理员可手动补）。
func grantGuestSubscription(ctx context.Context, repo *storage.TrafficRepository, username string) error {
	file, err := repo.GetSubscribeFileByFilename(ctx, guestSubscriptionFilename)
	if err != nil {
		logger.Info("[注册] 默认订阅不存在，跳过授权",
			"filename", guestSubscriptionFilename, "error", err)
		return nil
	}

	if err := repo.SetUserSubscriptions(ctx, username, []int64{file.ID}); err != nil {
		return err
	}
	logger.Info("[注册] 已自动授权订阅", "username", username, "filename", guestSubscriptionFilename)
	return nil
}
