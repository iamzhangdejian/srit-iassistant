package licenser

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/license"
)

// Handler 暴露授权相关的 HTTP 接口。
type Handler struct {
	service *Service
	// limiter 限制导入频率，防止有人刷验签吃满 CPU。
	limiter *applyLimiter
}

// NewHandler 构造处理器。service 为 nil 时所有接口都按「未启用」应答。
func NewHandler(service *Service) *Handler {
	return &Handler{service: service, limiter: newApplyLimiter()}
}

// licenseStatusResponse 是 GET /api/v1/license 的响应体。
//
// 字段命名对齐前端：camelCase 直出，避免前端再写一层映射。
type licenseStatusResponse struct {
	Enabled bool   `json:"enabled"`
	State   string `json:"state"`
	// Reason 是中文说明，前端直接展示。
	Reason string `json:"reason,omitempty"`
	// MachineFingerprint 与 BoundMACs 供用户找运维要码。
	MachineFingerprint string   `json:"machine_fingerprint"`
	LocalMACs          []string `json:"local_macs"`
	// PhysicalMACs 是物理网卡的出厂地址清单，带接口名与端口名。
	// 授权页主展示这一组；LocalMACs 是参与绑定校验的全集，保留给排查用。
	PhysicalMACs []physicalMACInfo `json:"physical_macs"`
	// BoundMACs 是当前授权码里绑的网卡，用于人工核对。
	BoundMACs []string `json:"bound_macs,omitempty"`
	// Features 是当前实际可用的功能位（过期降级后会被裁剪）。
	Features []string `json:"features"`
	// AllFeatures 是功能位的完整清单，前端据此渲染勾选框。
	AllFeatures []string `json:"all_features"`
	// ExpiresAt 是到期时间（Unix 秒），0 表示永久。
	ExpiresAt     int64 `json:"expires_at"`
	RemainingDays int   `json:"remaining_days"`
	// KeyID 是本机当前信任的公钥指纹；CodeKeyID 是已存码记录里记的
	// 签发者指纹。两者不一致（且 CodeKeyID 非空）说明这张码是用旧密钥
	// 签的——换钥事故的第一现场证据，用来把用户从「码被篡改」的误解里
	// 拽出来。
	KeyID     string `json:"key_id,omitempty"`
	CodeKeyID string `json:"code_key_id,omitempty"`
	// Serial / User / Org 用于确认「这是谁授权给谁」。
	Serial string `json:"serial,omitempty"`
	User   string `json:"user,omitempty"`
	Org    string `json:"org,omitempty"`
}

// physicalMACInfo 是物理网卡的展示信息。
type physicalMACInfo struct {
	Device string `json:"device"` // 接口名，如 en0
	Port   string `json:"port"`   // 硬件端口名，如 Wi-Fi
	MAC    string `json:"mac"`    // 出厂地址
}

// physicalMACInfos 取物理网卡清单，保证返回 [] 而不是 null。
func physicalMACInfos() []physicalMACInfo {
	items := license.PhysicalMACs()
	out := make([]physicalMACInfo, 0, len(items))
	for _, item := range items {
		out = append(out, physicalMACInfo{Device: item.Device, Port: item.Port, MAC: item.MAC})
	}
	return out
}

// featureStrings 把功能位转成字符串切片，保证是 [] 而不是 null。
func featureStrings(features []license.Feature) []string {
	out := make([]string, 0, len(features))
	for _, f := range features {
		out = append(out, string(f))
	}
	return out
}

// Status 处理 GET /api/v1/license。
//
// 这是一个「永远可访问」的接口：没有授权时它也必须能返回机器指纹，
// 否则用户既不知道该报什么码，也看不到该去哪导入。
func (h *Handler) Status(c *gin.Context) {
	if h.service == nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": licenseStatusResponse{
			Enabled: false, State: string(license.StateValid),
			Features:    featureStrings(license.AllFeatures),
			AllFeatures: featureStrings(license.AllFeatures),
		}})
		return
	}

	ev := h.service.Status()
	fp, macs := h.service.MachineFingerprint()

	resp := licenseStatusResponse{
		Enabled:            h.service.Enabled(),
		State:              string(ev.State),
		Reason:             ev.Reason,
		MachineFingerprint: fp,
		LocalMACs:          macs,
		PhysicalMACs:       physicalMACInfos(),
		KeyID:              h.service.KeyID(),
		CodeKeyID:          h.service.RecordedKeyID(),
		Features:           featureStrings(ev.Features),
		AllFeatures:        featureStrings(license.AllFeatures),
		ExpiresAt:          ev.ExpiresAt,
		RemainingDays:      ev.RemainingDays,
	}
	if ev.Payload != nil {
		resp.BoundMACs = ev.Payload.MAC
		resp.Serial = ev.Payload.SN
		resp.User = ev.Payload.User
		resp.Org = ev.Payload.Org
	}
	if !h.service.Enabled() {
		// 闸门关闭时对外报告「全部功能可用」，让前端不必写两套逻辑。
		resp.State = string(license.StateValid)
		resp.Features = featureStrings(license.AllFeatures)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": resp})
}

type applyRequest struct {
	Code string `json:"code" binding:"required"`
}

// Apply 处理 POST /api/v1/license。
//
// 导入成功后会立刻刷新缓存，用户不必重启应用就能继续操作。
func (h *Handler) Apply(c *gin.Context) {
	if h.service == nil {
		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}

	// 先限流再解析 JSON：解析本身也要花时间，且限流应在进入任何
	// 业务逻辑之前生效。
	if h.limiter != nil && !h.limiter.allow() {
		c.Header("Retry-After", strconv.Itoa(int(h.limiter.retryAfter().Seconds())+1))
		c.JSON(http.StatusTooManyRequests, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "LICENSE_RATE_LIMITED",
				"message": "导入授权码过于频繁，请稍后再试",
			},
		})
		return
	}

	var req applyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"message": "请粘贴完整的授权码"},
		})
		return
	}

	if _, err := h.service.Apply(req.Code); err != nil {
		// Apply 返回的都是 *apperrors.AppError，里面已经带了中文 message
		// 和该用的 HTTP 码，直接取字段即可，不必再翻译一遍——两处文案
		// 各写一次迟早会不一致。
		var appErr *apperrors.AppError
		if errors.As(err, &appErr) {
			c.JSON(appErr.HTTPCode, gin.H{
				"success": false,
				"error":   gin.H{"message": appErr.Message, "code": appErr.Code},
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"message": err.Error()},
		})
		return
	}

	// 导入后立刻重新判定，把新状态回给前端，省一次往返。
	h.service.Refresh()
	h.Status(c)
}
