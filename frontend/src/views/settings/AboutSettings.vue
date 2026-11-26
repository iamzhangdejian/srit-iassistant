<template>
  <div class="about-settings">
    <div class="section-header">
      <h2>关于</h2>
      <p class="section-description">
        授权状态、本机标识与软件版本。换机器或续期时都在这一页处理。
      </p>
    </div>

    <div class="about-grid">
      <!-- 授权状态 -->
      <section v-if="licenseEnabled" class="about-card about-card--status" :class="`is-${stateKey}`">
        <div class="about-card__head">
          <span class="about-card__title">授权状态</span>
          <t-button size="small" variant="text" :loading="refreshing" @click="refresh">
            刷新
          </t-button>
        </div>

        <div class="about-status">
          <span class="about-status__dot" aria-hidden="true"></span>
          <span class="about-status__value">{{ stateLabel }}</span>
          <span v-if="remainingText" class="about-status__meta">{{ remainingText }}</span>
        </div>

        <p class="about-card__note">{{ stateDescription }}</p>

        <div v-if="statusFacts.length" class="about-facts">
          <span v-for="fact in statusFacts" :key="fact" class="about-fact">{{ fact }}</span>
        </div>

        <div v-if="stateKey === 'grace' || stateKey === 'readonly'" class="about-card__cta">
          <t-button size="small" theme="primary" variant="outline" @click="focusRenew">
            立即续期
          </t-button>
        </div>
      </section>

      <!-- 授权详情：只渲染有值的字段 -->
      <section v-if="licenseEnabled && licenseRows.length" class="about-card">
        <div class="about-card__head">
          <span class="about-card__title">授权详情</span>
        </div>

        <div class="about-kv">
          <template v-for="row in licenseRows" :key="row.key">
            <span class="about-kv__k">{{ row.label }}</span>
            <span class="about-kv__v">{{ row.value }}</span>
          </template>
        </div>

        <div v-if="grantedFeatures.length" class="about-tags">
          <span v-for="f in grantedFeatures" :key="f.key" class="about-tag">{{ f.label }}</span>
        </div>
      </section>

      <!-- 本机标识 -->
      <section v-if="licenseEnabled && (fingerprint || macs.length)" class="about-card">
        <div class="about-card__head">
          <span class="about-card__title">本机标识</span>
          <span class="about-card__hint">发给运维即可申请授权码</span>
        </div>

        <!-- 指纹与网卡同一行样式：标签列定宽、号码列等宽、复制按钮靠右 -->
        <div class="about-hosts">
          <div v-if="fingerprint" class="about-host">
            <span class="about-host__label">机器指纹</span>
            <code class="about-host__value mono" :title="fingerprint">{{ fingerprint }}</code>
            <t-button class="about-host__copy" size="small" variant="text" @click="copy(fingerprint, '机器指纹')">
              复制
            </t-button>
          </div>
          <div v-for="mac in macs" :key="mac.mac" class="about-host">
            <span class="about-host__label">{{ macLabel(mac) }}</span>
            <code class="about-host__value mono">{{ mac.mac }}</code>
            <t-button class="about-host__copy" size="small" variant="text" @click="copy(mac.mac, '网卡地址')">
              复制
            </t-button>
          </div>
        </div>
      </section>

      <!-- 软件版本：只留交付现场真正用得上的字段 -->
      <section class="about-card">
        <div class="about-card__head">
          <span class="about-card__title">软件版本</span>
        </div>

        <div v-if="versionRows.length" class="about-kv">
          <template v-for="row in versionRows" :key="row.key">
            <span class="about-kv__k">{{ row.label }}</span>
            <span class="about-kv__v">
              <span class="mono">{{ row.value }}</span>
              <t-tag v-if="row.tag" size="small" variant="light" :theme="row.tagTheme || 'default'">
                {{ row.tag }}
              </t-tag>
            </span>
          </template>
        </div>
        <p v-else class="about-card__note">正在读取版本信息…</p>
      </section>

      <!-- 续期 -->
      <section v-if="licenseEnabled" ref="renewCard" class="about-card about-card--wide">
        <div class="about-card__head">
          <span class="about-card__title">续期 / 更换授权码</span>
        </div>

        <p class="about-card__note">
          授权码绑定本机的机器指纹。同一台机器续期只需粘贴新码，导入后立即生效；换机器需要重新签发。
        </p>

        <div class="about-renew">
          <t-textarea
            v-model="code"
            placeholder="粘贴运维提供的授权码（WNL1....）"
            :autosize="{ minRows: 2, maxRows: 4 }"
            @keydown.ctrl.enter.prevent="handleImport"
            @keydown.meta.enter.prevent="handleImport"
          />
          <div class="about-renew__actions">
            <t-button theme="primary" :loading="binding" :disabled="!code.trim()" @click="handleImport">
              导入
            </t-button>
            <span v-if="error" class="about-renew__error">{{ error }}</span>
          </div>
        </div>
      </section>
    </div>

    <t-alert
      v-if="systemInfo?.db_migration_error"
      theme="error"
      title="数据库迁移未完成"
      class="about-db-alert"
    >
      <pre class="about-db-alert__detail">{{ systemInfo.db_migration_error }}</pre>
    </t-alert>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { FEATURE_LABELS, LICENSE_FEATURES } from '@/api/license'
import { bindLicense, ensureLicenseStatus, refreshLicenseStatus, useLicense } from '@/composables/useLicense'
import { useEditorResourcesStore } from '@/stores/editorResources'

const license = useLicense()
const editor = useEditorResourcesStore()

const code = ref('')
const binding = ref(false)
const refreshing = ref(false)
const error = ref('')
const renewCard = ref<HTMLElement | null>(null)

const frontendVersion = __FRONTEND_VERSION__
const frontendCommit = __FRONTEND_COMMIT__

const status = computed(() => license.status.value)
const licenseEnabled = computed(() => license.enabled.value !== false)
const stateKey = computed(() => status.value?.state || 'missing')
const fingerprint = computed(() => status.value?.machine_fingerprint || '')
const systemInfo = computed(() => editor.systemInfo as Record<string, any> | null)

const STATE_LABELS: Record<string, string> = {
  valid: '已授权',
  grace: '宽限期',
  readonly: '只读',
  missing: '未授权',
  invalid: '授权无效',
}

const STATE_DESCRIPTIONS: Record<string, string> = {
  valid: '授权有效，全部已开通功能可用。',
  grace: '已到期但仍在 7 天宽限期内，功能正常，建议尽快续期。',
  readonly: '已过期，转为只读：历史资料可查看，文档生成等写操作已停用。',
  missing: '尚未导入授权码，业务接口会被拒绝。',
  invalid: '授权码未通过校验（机器不符、签名错误或系统时间异常）。',
}

const stateLabel = computed(() => STATE_LABELS[stateKey.value] || '未知')
const stateDescription = computed(() => {
  const s = status.value
  if (!s) return '正在读取授权状态…'
  if (s.state === 'invalid' && s.reason) return s.reason
  return STATE_DESCRIPTIONS[s.state] || ''
})

const remainingText = computed(() => {
  const s = status.value
  if (!s || s.state === 'missing') return ''
  if (s.expires_at === 0) return '永久有效'
  if (typeof s.remaining_days === 'number' && s.remaining_days >= 0) {
    if (s.state === 'readonly') return '已过期'
    return `剩余 ${s.remaining_days} 天`
  }
  return ''
})

const expiryText = computed(() => {
  const s = status.value
  if (!s) return ''
  if (s.expires_at === 0) return '永久有效'
  const d = new Date(s.expires_at * 1000)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
})

// 只渲染有值的字段，避免交付现场出现一排「—」。
const licenseRows = computed(() => {
  const s = status.value
  if (!s) return [] as { key: string; label: string; value: string }[]
  const rows: { key: string; label: string; value: string }[] = []
  if (s.serial) rows.push({ key: 'serial', label: '序列号', value: s.serial })
  if (s.user) rows.push({ key: 'user', label: '使用人', value: s.user })
  if (s.org) rows.push({ key: 'org', label: '单位', value: s.org })
  if (expiryText.value) rows.push({ key: 'expiry', label: '有效期', value: expiryText.value })
  return rows
})

// 状态卡底部的事实条：只放有信息量的短句，不做成键值表（详情卡已经有了）。
const statusFacts = computed(() => {
  const s = status.value
  if (!s) return []
  const facts: string[] = []
  if (expiryText.value) facts.push(`有效期至 ${expiryText.value}`)
  const count = (s.features || []).length
  if (count > 0) facts.push(`已开通 ${count} 项功能`)
  if (s.serial) facts.push(`序列号 ${s.serial}`)
  return facts
})

const grantedFeatures = computed(() =>
  LICENSE_FEATURES
    .filter((key) => (status.value?.features || []).includes(key))
    .map((key) => ({ key, label: (FEATURE_LABELS as Record<string, string>)[key] || key })),
)

const macs = computed(() => {
  const list = status.value?.physical_macs
  if (list?.length) return list
  return (status.value?.local_macs || []).map((mac) => ({ device: '', port: '', mac }))
})

const macLabel = (mac: { device?: string; port?: string }) => {
  if (mac.device && mac.port) return `${mac.device} / ${mac.port}`
  return mac.device || mac.port || '网卡'
}

const formatDateTime = (value?: string) => {
  if (!value) return ''
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return String(value)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

const shortCommit = (value?: string) => {
  if (!value || value === 'unknown') return ''
  return value.length > 12 ? value.slice(0, 12) : value
}

const versionRows = computed(() => {
  const info = systemInfo.value
  if (!info) return [] as { key: string; label: string; value: string; tag?: string; tagTheme?: string }[]

  const rows: { key: string; label: string; value: string; tag?: string; tagTheme?: string }[] = []
  const backendVersion = info.version && info.version !== 'unknown' ? String(info.version) : ''
  if (backendVersion) {
    rows.push({
      key: 'version',
      label: '应用版本',
      value: backendVersion,
      tag: info.edition === 'lite' ? '桌面版' : '',
      tagTheme: 'primary',
    })
  }
  if (info.build_time) rows.push({ key: 'build', label: '构建时间', value: formatDateTime(String(info.build_time)) })
  const commit = shortCommit(info.commit_id)
  if (commit) rows.push({ key: 'commit', label: '构建标识', value: commit })

  // 前端版本只在和后端不一致时才有信息量，平时不占一行。
  if (
    frontendVersion &&
    frontendVersion !== 'unknown' &&
    backendVersion &&
    frontendVersion !== backendVersion
  ) {
    rows.push({
      key: 'frontend',
      label: '前端版本',
      value: frontendVersion,
      tag: '与后端不一致',
      tagTheme: 'warning',
    })
  }
  if (info.db_version) {
    // SQLite 这一侧后端报的是迁移序号（例如 53），直接显示「53」没意义
    const raw = String(info.db_version)
    rows.push({
      key: 'db',
      label: '数据库版本',
      value: /^\d+$/.test(raw) ? `SQLite · schema ${raw}` : raw,
    })
  }
  return rows
})

const copy = async (text: string, label: string) => {
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    MessagePlugin.success(`${label}已复制`)
  } catch {
    MessagePlugin.error('复制失败，请手动选中复制')
  }
}

const refresh = async () => {
  refreshing.value = true
  try {
    await refreshLicenseStatus()
  } finally {
    refreshing.value = false
  }
}

const focusRenew = async () => {
  await nextTick()
  renewCard.value?.scrollIntoView({ behavior: 'smooth', block: 'center' })
  const textarea = renewCard.value?.querySelector('textarea') as HTMLTextAreaElement | null
  textarea?.focus()
}

const handleImport = async () => {
  const value = code.value.trim()
  if (!value || binding.value) return
  binding.value = true
  error.value = ''
  try {
    await bindLicense(value)
    await refreshLicenseStatus()
    code.value = ''
    MessagePlugin.success('授权已更新')
  } catch (err: any) {
    error.value = err?.message || '导入失败，请检查授权码是否完整'
  } finally {
    binding.value = false
  }
}

onMounted(() => {
  if (license.status.value === null) void ensureLicenseStatus()
  void editor.ensureSystemInfo()
})
</script>

<style scoped lang="less">
.about-settings {
  width: 100%;
}

.about-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 16px;
  margin-top: 4px;
}

.about-card {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
  padding: 18px 20px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-large, 12px);
  background: var(--td-bg-color-container);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.03);

  &--wide {
    grid-column: 1 / -1;
  }

  &__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    min-height: 22px;
  }

  &__title {
    font-size: 13px;
    font-weight: 600;
    color: var(--td-text-color-secondary);
    letter-spacing: 0.02em;
  }

  &__hint {
    font-size: 12px;
    color: var(--td-text-color-placeholder);
  }

  &__note {
    margin: 0;
    font-size: 12px;
    line-height: 1.7;
    color: var(--td-text-color-placeholder);
  }

  &__cta {
    margin-top: 2px;
  }
}

/* 状态卡：左侧一条语义色，不做大面积上色 */
.about-card--status {
  position: relative;
  padding-left: 22px;

  &::before {
    content: '';
    position: absolute;
    left: 0;
    top: 14px;
    bottom: 14px;
    width: 3px;
    border-radius: 0 3px 3px 0;
    background: var(--td-success-color);
  }

  &.is-grace::before,
  &.is-readonly::before {
    background: var(--td-warning-color);
  }

  &.is-missing::before,
  &.is-invalid::before {
    background: var(--td-error-color);
  }
}

.about-status {
  display: flex;
  align-items: baseline;
  gap: 10px;
  min-width: 0;

  &__dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--td-success-color);
    transform: translateY(-2px);
  }

  &__value {
    font-size: 22px;
    font-weight: 600;
    line-height: 1.2;
    color: var(--td-text-color-primary);
  }

  &__meta {
    font-size: 12px;
    color: var(--td-text-color-placeholder);
  }
}

.is-grace .about-status__dot,
.is-readonly .about-status__dot {
  background: var(--td-warning-color);
}

.is-missing .about-status__dot,
.is-invalid .about-status__dot {
  background: var(--td-error-color);
}

.about-kv {
  display: grid;
  grid-template-columns: 76px minmax(0, 1fr);
  row-gap: 10px;
  column-gap: 12px;
  font-size: 13px;

  &__k {
    color: var(--td-text-color-placeholder);
  }

  &__v {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
    color: var(--td-text-color-primary);
    overflow-wrap: anywhere;
  }
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}

.ellipsis {
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.about-facts {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 14px;
  margin-top: auto;
}

.about-fact {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.about-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.about-tag {
  padding: 1px 10px;
  border-radius: 999px;
  font-size: 11px;
  line-height: 20px;
  color: var(--td-brand-color);
  background: var(--td-brand-color-light);
}

.about-hosts {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.about-host {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  min-width: 0;

  &__label {
    flex: 0 0 96px;
    color: var(--td-text-color-placeholder);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &__value {
    flex: 1 1 auto;
    min-width: 0;
    color: var(--td-text-color-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &__copy {
    flex: 0 0 auto;
  }
}

.about-renew {
  display: flex;
  flex-direction: column;
  gap: 10px;

  &__actions {
    display: flex;
    align-items: center;
    gap: 12px;
    flex-wrap: wrap;
  }

  &__error {
    font-size: 12px;
    color: var(--td-error-color);
  }
}

.about-db-alert {
  margin-top: 16px;

  &__detail {
    margin: 0;
    font-size: 12px;
    white-space: pre-wrap;
    word-break: break-all;
  }
}

@media (max-width: 720px) {
  .about-grid {
    grid-template-columns: minmax(0, 1fr);
  }

  .about-kv {
    grid-template-columns: 68px minmax(0, 1fr);
  }
}
</style>