<template>
  <div class="gate-page">
    <div class="gate-card">
      <header class="gate-head">
        <img :src="BRAND.logoPng" :alt="BRAND.name" class="gate-logo" />
        <h1 class="gate-title">{{ gateTitle }}</h1>
        <p class="gate-subtitle">{{ gateSubtitle }}</p>
      </header>

      <!-- 授权无效：把后端原因摊开讲，避免用户重复绑同一个码 -->
      <p v-if="showReason" class="gate-reason">{{ reason }}</p>

      <!-- 已授权：静默建会话后直接进系统，不留可点的「登录」按钮 -->
      <section v-if="!licenseRequired" class="gate-section gate-enter">
        <div v-if="!enterError" class="gate-progress">
          <t-loading size="small" />
          <span>正在进入系统…</span>
        </div>
        <template v-else>
          <t-alert theme="error" :message="enterError">
            <template #operation>
              <t-button size="small" @click="enterSystem">重试</t-button>
            </template>
          </t-alert>
          <p class="gate-hint">
            授权已生效，但无法自动建立本机会话。请确认通过桌面版应用启动（浏览器里直接打开前端不支持自动登录）。
          </p>
        </template>
      </section>

      <!-- 未授权：唯一入口，绑定后直接进入 -->
      <template v-else>
        <section class="gate-section">
          <h2 class="gate-section__title">本机标识</h2>
          <p class="gate-section__hint">把任意一条网卡地址或机器指纹发给运维，获取对应的授权码。</p>
          <div class="gate-hosts">
            <div v-for="item in physicalMACs" :key="`${item.device}-${item.mac}`" class="gate-host">
              <span class="gate-host__label">{{ [item.port, item.device].filter(Boolean).join(' / ') || '网卡' }}</span>
              <code class="gate-host__value">{{ item.mac }}</code>
              <t-button class="gate-host__copy" size="small" variant="text" @click="copyText(item.mac, '网卡地址')">
                复制
              </t-button>
            </div>
            <p v-if="!physicalMACs.length" class="gate-hint">
              未读取到物理网卡。虚拟机或容器环境请让运维用「自定义 MAC 锚点」签发。
            </p>

            <!-- 指纹与网卡同一行样式，地址列、复制按钮列都对齐 -->
            <div class="gate-host">
              <span class="gate-host__label">机器指纹</span>
              <code class="gate-host__value" :title="fingerprint || ''">{{ fingerprint || '—' }}</code>
              <t-button
                class="gate-host__copy"
                size="small"
                variant="text"
                :disabled="!fingerprint"
                @click="copyText(fingerprint, '机器指纹')"
              >
                复制
              </t-button>
            </div>
          </div>
          <!-- 签发密钥指纹：把「本机认谁签的」直接摆出来。
               换钥事故里，用户看到的是「授权码无效或已被篡改」，而真正
               的原因是这张码由另一把私钥签发；不显示指纹就只能靠猜。 -->
          <p v-if="keyId" class="gate-hint gate-keyhint">
            本机信任的签发密钥：<code>{{ keyId }}</code>
            <template v-if="keyMismatch">
              ，但现有授权码记录的签发密钥是 <code>{{ codeKeyId }}</code> —— 两者不一致，
              说明这张码是用旧密钥签发的，请向运维索取用新密钥重签的授权码。
            </template>
          </p>
        </section>

        <section class="gate-section">
          <h2 class="gate-section__title">授权码</h2>
          <p class="gate-section__hint">粘贴运维提供的授权码，校验通过即进入系统。</p>
          <t-textarea
            v-model="licenseCode"
            class="gate-input"
            placeholder="WNL1...."
            :autosize="{ minRows: 3, maxRows: 6 }"
            :disabled="binding"
            @keydown.ctrl.enter="handleBind"
            @keydown.meta.enter="handleBind"
          />
          <p v-if="licenseError" class="gate-error">{{ licenseError }}</p>
          <t-button
            theme="primary"
            size="large"
            block
            class="gate-submit"
            :loading="binding"
            :disabled="!licenseCode.trim()"
            @click="handleBind"
          >
            绑定并进入
          </t-button>
        </section>
      </template>

      <footer class="gate-foot">
        <span>{{ BRAND.copyright }}</span>
        <span class="gate-foot__sep">·</span>
        <span>{{ BRAND.name }}</span>
      </footer>
    </div>
  </div>
</template>

<script setup lang="ts">
// 唯一入口页：授权码即凭证，不再有账号密码、注册、找回密码。
//
// 为什么保留「静默建会话」这一步：后端所有接口都挂在 JWT 上，前端必须
// 拿到会话才能工作。区别只在于凭证从哪来——以前是用户输入邮箱密码，
// 现在是桌面桥拿一次性令牌换，用户全程看不到。
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { BRAND, OEM_HOME_ROUTE } from '@/config/oem'
import { bindLicense, ensureLicenseStatus, useLicense } from '@/composables/useLicense'
import { ensureDesktopSession } from '@/utils/desktopSession'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const authStore = useAuthStore()
const license = useLicense()

const licenseCode = ref('')
const binding = ref(false)
const licenseError = ref('')
const enterError = ref('')

const licenseEnabled = computed(() => license.enabled.value)
// 服务端/开发形态不启用授权：直接放行进入，不显示绑定卡。
const licenseRequired = computed(() => licenseEnabled.value && license.blocked.value)
const state = computed(() => license.status.value?.state || 'missing')
const reason = computed(() => license.status.value?.reason || '')
const showReason = computed(() => licenseRequired.value && state.value === 'invalid' && !!reason.value)
const fingerprint = computed(() => license.status.value?.machine_fingerprint || '')
// 签发密钥指纹。keyId 是本机内置公钥，codeKeyId 是现存授权码记录里记的
// 签发者；两者不一致就是「码是旧钥签的」的确证。
const keyId = computed(() => license.status.value?.key_id || '')
const codeKeyId = computed(() => license.status.value?.code_key_id || '')
const keyMismatch = computed(() => !!keyId.value && !!codeKeyId.value && keyId.value !== codeKeyId.value)

const gateTitle = computed(() => {
  if (!licenseEnabled.value) return '正在进入系统'
  if (licenseRequired.value) return '绑定授权'
  return '正在进入系统'
})

const gateSubtitle = computed(() => {
  if (licenseRequired.value) return '本机尚未授权，导入授权码后即可使用'
  if (state.value === 'readonly') return '授权已过期，以只读方式进入'
  if (state.value === 'grace') return '授权已到期，宽限期内仍可正常使用'
  return '授权校验通过，无需账号登录'
})

// 只展示物理网卡：后端已筛掉虚拟网卡（utun/bridge 等），前端不二次猜，
// 否则会出现「界面给的地址校验不过」。
const physicalMACs = computed(() => {
  const list = license.status.value?.physical_macs
  if (list?.length) return list
  return (license.status.value?.local_macs || []).map((mac) => ({ device: '', port: '', mac }))
})

const copyText = async (text: string, label: string) => {
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    MessagePlugin.success(`${label}已复制`)
  } catch {
    MessagePlugin.error('复制失败，请手动选中复制')
  }
}

const landAfterEnter = () => {
  router.replace(authStore.hasValidTenant ? OEM_HOME_ROUTE : '/onboarding/workspace')
}

// 进入系统：授权已通过时，会话靠桌面桥静默建立。
const enterSystem = async () => {
  enterError.value = ''
  const ok = await ensureDesktopSession()
  if (!ok) {
    enterError.value = '无法建立本机会话'
    return
  }
  landAfterEnter()
}

const handleBind = async () => {
  if (!licenseCode.value.trim() || binding.value) return
  binding.value = true
  licenseError.value = ''
  try {
    await bindLicense(licenseCode.value)
    // 绑定成功后授权闸门可能从 invalid 变 valid，重新取一次状态再进系统，
    // 避免守卫手里还是旧状态把用户弹回来。
    await ensureLicenseStatus(true)
    const ok = await ensureDesktopSession()
    if (!ok) {
      enterError.value = '授权已生效，但无法建立本机会话'
      return
    }
    licenseCode.value = ''
    MessagePlugin.success('授权已生效')
    landAfterEnter()
  } catch (error: any) {
    licenseError.value = error?.message || '授权码校验失败，请检查后重试'
  } finally {
    binding.value = false
  }
}

onMounted(async () => {
  await ensureLicenseStatus()
  // 已授权（含只读/宽限）→ 不再让用户点任何东西，直接进系统。
  if (!license.blocked.value) {
    await enterSystem()
  }
})
</script>

<style scoped>
.gate-page {
  box-sizing: border-box;
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100vh;
  height: 100dvh;
  padding: 24px;
  overflow-y: auto;
  background:
    radial-gradient(1200px 600px at 15% 10%, rgba(0, 168, 112, 0.16), transparent 60%),
    radial-gradient(900px 500px at 85% 90%, rgba(0, 120, 200, 0.12), transparent 60%),
    var(--td-bg-color-page);
}

.gate-card {
  box-sizing: border-box;
  width: 100%;
  max-width: 560px;
  padding: 28px 30px 22px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-large, 12px);
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.08);
}

.gate-head {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  gap: 6px;
}

.gate-logo {
  width: 56px;
  height: 56px;
  object-fit: contain;
}

.gate-title {
  margin: 6px 0 0;
  font-size: 22px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.gate-subtitle {
  margin: 0;
  font-size: 13px;
  color: var(--td-text-color-secondary);
}

.gate-keyhint {
  /* 比普通提示更靠上、更显眼一点：这一行是排障时最先要读的信息 */
  margin-top: 10px;
  line-height: 1.7;
  word-break: break-all;
}

.gate-keyhint code {
  /* 指纹是一串十六进制，等宽字体才能逐位比对 */
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  padding: 1px 5px;
  border-radius: 4px;
  background: rgba(0, 0, 0, 0.06);
}

.gate-reason {
  margin: 14px 0 0;
  padding: 8px 12px;
  border-radius: 8px;
  background: var(--td-warning-color-light, rgba(237, 123, 47, 0.1));
  color: var(--td-warning-color, #ed7b2f);
  font-size: 13px;
  line-height: 1.5;
}

.gate-section {
  padding: 18px 0;
  border-top: 1px solid var(--td-component-stroke);
  margin-top: 18px;
}

.gate-section__title {
  margin: 0 0 4px;
  font-size: 14px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.gate-section__hint {
  margin: 0 0 10px;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  line-height: 1.6;
}

.gate-hosts {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

/* 网卡与指纹共用同一行样式：标签列定宽、地址列等宽、复制按钮靠右，三列严格对齐 */
.gate-host {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 10px;
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer, #f3f3f3);
}

.gate-host__label {
  flex: 0 0 128px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.gate-host__value {
  flex: 1 1 auto;
  min-width: 0;
  font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: 12px;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.gate-host__copy {
  flex: 0 0 auto;
}

.gate-input {
  width: 100%;
  margin-bottom: 10px;
}

.gate-error {
  margin: 0 0 10px;
  color: var(--td-error-color, #d54941);
  font-size: 12px;
}

.gate-submit {
  margin-top: 2px;
}

.gate-enter {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.gate-progress {
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--td-text-color-secondary);
  font-size: 13px;
}

.gate-hint {
  margin: 8px 0 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  line-height: 1.6;
}

.gate-foot {
  display: flex;
  justify-content: center;
  gap: 6px;
  margin-top: 18px;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

/* 矮窗口：入口页是「一屏办完一件事」的页面，宁可压缩留白，也不要
   出现滚动条——授权码输入框被推到屏幕外是最容易出错的交互。 */
@media (max-height: 800px) {
  .gate-page {
    padding: 16px;
  }

  .gate-card {
    padding: 20px 22px 16px;
  }

  .gate-section {
    padding: 14px 0;
    margin-top: 14px;
  }

  .gate-logo {
    width: 44px;
    height: 44px;
  }

  .gate-title {
    font-size: 20px;
  }
}

@media (max-height: 700px) {
  .gate-page {
    padding: 12px;
  }

  .gate-card {
    padding: 16px 18px 12px;
  }

  .gate-logo {
    width: 36px;
    height: 36px;
  }

  .gate-title {
    font-size: 18px;
    margin-top: 2px;
  }

  .gate-subtitle {
    font-size: 12px;
  }

  .gate-section {
    padding: 10px 0;
    margin-top: 10px;
  }

  .gate-section__hint {
    display: none;
  }

  .gate-hosts {
    gap: 4px;
  }

  .gate-host {
    padding: 4px 8px;
  }

  .gate-host__label {
    flex-basis: 104px;
  }

  .gate-input {
    margin-bottom: 8px;
  }

  .gate-input :deep(textarea) {
    height: 56px !important;
    min-height: 56px;
  }

  .gate-foot {
    margin-top: 10px;
    font-size: 11px;
  }
}

@media (max-height: 600px) {
  .gate-page {
    padding: 8px;
  }

  .gate-head {
    flex-direction: row;
    justify-content: center;
    gap: 10px;
    text-align: left;
    flex-wrap: wrap;
  }

  .gate-logo {
    width: 28px;
    height: 28px;
  }

  .gate-title {
    font-size: 16px;
    margin: 0;
  }

  .gate-subtitle {
    flex-basis: 100%;
    text-align: center;
  }

  .gate-section {
    padding: 8px 0;
    margin-top: 8px;
  }

  .gate-input :deep(textarea) {
    height: 48px !important;
    min-height: 48px;
  }

  .gate-foot {
    margin-top: 8px;
  }
}
</style>