import { reactive, ref, computed, watch } from 'vue'
import { defineStore } from 'pinia'
import i18n from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities'
import { useLicense } from '@/composables/useLicense'
import type { DeploymentCapabilityKey } from '@/config/deploymentCapabilities'
import type { QuestionOrigin } from '@/utils/questionOrigin'
// OEM: 菜单白名单与自定义标题
import { OEM_MENU_ALL, OEM_MENU_FEATURE, OEM_MENU_TITLES } from '@/config/oem'

type MenuChild = Record<string, any>

interface MenuItem {
  title: string
  titleKey?: string
  icon: string
  path: string
  childrenPath?: string
  children?: MenuChild[]
  requiredCapability?: DeploymentCapabilityKey
}

const createMenuChildren = () => reactive<MenuChild[]>([])

export const useMenuStore = defineStore('menuStore', () => {
  const menuArr = reactive<MenuItem[]>([
    {
      title: '',
      titleKey: 'menu.newChat',
      icon: 'prefixIcon',
      path: 'creatChat',
      childrenPath: 'chat',
      children: createMenuChildren()
    },
    { title: '', titleKey: 'menu.knowledgeBase', icon: 'zhishiku', path: 'knowledge-bases' },
    // Artifacts only exist where skills run in a sandbox.
    { title: '', titleKey: 'menu.artifacts', icon: 'artifact', path: 'artifacts', requiredCapability: 'settings.sandbox' },
    { title: '', titleKey: 'menu.agents', icon: 'agent', path: 'agents', requiredCapability: 'agents' },
    { title: '', titleKey: 'menu.automations', icon: 'automation', path: 'automations' },
    { title: '', titleKey: 'toolbox.title', icon: 'toolbox', path: 'toolbox' },
    { title: '', titleKey: 'menu.organizations', icon: 'organization', path: 'organizations', requiredCapability: 'organizations' },
    { title: '', titleKey: 'menu.settings', icon: 'setting', path: 'settings' },
    { title: '', titleKey: 'menu.logout', icon: 'logout', path: 'logout' }
  ])

  const isFirstSession = ref(false)
  const firstQuery = ref('')
  const firstMentionedItems = ref<any[]>([])
  const firstModelId = ref('')
  const firstImageFiles = ref<any[]>([])
  const firstAttachmentFiles = ref<any[]>([])
  const firstQuestionOrigin = ref<QuestionOrigin | null>(null)
  const prefillQuery = ref('')

  const applyMenuTranslations = () => {
    menuArr.forEach(item => {
      // OEM: 优先使用 OEM_MENU_TITLES 里的自定义标题（如「对话问答」「模型管理」），
      //      未配置时回落到 i18n 默认文案。
      const oemTitle = OEM_MENU_TITLES[item.path]
      if (oemTitle) {
        item.title = oemTitle
      } else if (item.titleKey) {
        item.title = i18n.global.t(item.titleKey)
      }
    })
  }

  applyMenuTranslations()

  watch(
    () => i18n.global.locale.value,
    () => {
      applyMenuTranslations()
    }
  )

  const liteHiddenPaths = new Set(['logout', 'organizations'])

  // 共享空间 (organizations) 仅对当前空间的 admin / owner 暴露入口。
  // viewer / contributor 即便在共享空间里拥有资源，也无需自行管理共享关系，
  // 入口在侧栏只会徒增噪音；后端 RBAC 才是权限的最终来源（见 middleware/rbac.go）。
  const visibleMenuArr = computed(() => {
    const authStore = useAuthStore()
    const deploymentCapabilities = useDeploymentCapabilitiesStore()
    const license = useLicense()
    const whitelist = new Set(OEM_MENU_ALL)
    return menuArr.filter(item => {
      // OEM: 白名单之外的一律不渲染。
      // 这是产品形态的唯一开关——想恢复某个菜单，只需把它加回
      // frontend/src/config/oem.ts 的 OEM_MENU_WHITELIST（logout 放 OEM_MENU_BOTTOM）。
      // 注意：菜单隐藏只是 UI 层，配套的接口已在后端屏蔽
      //      （见 internal/router/router.go 的 FeatCut* 开关），
      //      所以即便用户直接拼 URL 也调不到被裁掉的能力。
      if (!whitelist.has(item.path)) {
        return false
      }
      if (authStore.isLiteMode && liteHiddenPaths.has(item.path)) {
        return false
      }
      if (item.path === 'organizations' && !authStore.hasRole('admin')) {
        return false
      }
      if (!deploymentCapabilities.isSupported(item.requiredCapability)) {
        return false
      }
      // 授权功能位收敛：没买的功能不显示入口。
      //
      // 与上面的白名单是两层过滤，互不替代：白名单决定「这个产品形态
      // 有没有这个菜单」，功能位决定「这台机器买了没有」。
      const requiredFeature = OEM_MENU_FEATURE[item.path]
      if (requiredFeature && !license.canUse(requiredFeature)) {
        return false
      }
      return true
    })
  })

  const chatMenuIndex = menuArr.findIndex(item => item.path === 'creatChat')

  const clearMenuArr = () => {
    const chatMenu = menuArr[chatMenuIndex]
    if (chatMenu && chatMenu.children) {
      chatMenu.children = createMenuChildren()
    }
  }

  const updatemenuArr = (obj: any) => {
    const chatMenu = menuArr[chatMenuIndex]
    if (!chatMenu.children) {
      chatMenu.children = createMenuChildren()
    }
    const exists = chatMenu.children.some((item: MenuChild) => item.id === obj.id)
    if (!exists) {
      chatMenu.children.push(obj)
    }
  }

  const updataMenuChildren = (item: MenuChild) => {
    const chatMenu = menuArr[chatMenuIndex]
    if (!chatMenu.children) {
      chatMenu.children = createMenuChildren()
    }
    chatMenu.children.unshift(item)
  }

  const updatasessionTitle = (sessionId: string, title: string) => {
    const chatMenu = menuArr[chatMenuIndex]
    chatMenu.children?.forEach((item: MenuChild) => {
      if (item.id === sessionId) {
        item.title = title
        item.isNoTitle = false
      }
    })
  }

  const changeIsFirstSession = (payload: boolean) => {
    isFirstSession.value = payload
  }

  const changeFirstQuery = (payload: string, mentionedItems: any[] = [], modelId: string = '', imageFiles: any[] = [], attachmentFiles: any[] = [], questionOrigin: QuestionOrigin | null = null) => {
    firstQuery.value = payload
    firstMentionedItems.value = mentionedItems
    firstModelId.value = modelId
    firstImageFiles.value = imageFiles
    firstAttachmentFiles.value = attachmentFiles
    firstQuestionOrigin.value = questionOrigin
  }

  const setPrefillQuery = (q: string) => {
    prefillQuery.value = q
  }

  const consumePrefillQuery = () => {
    const q = prefillQuery.value
    prefillQuery.value = ''
    return q
  }

  return {
    menuArr,
    visibleMenuArr,
    isFirstSession,
    firstQuery,
    firstMentionedItems,
    firstModelId,
    firstImageFiles,
    firstAttachmentFiles,
    firstQuestionOrigin,
    prefillQuery,
    clearMenuArr,
    updatemenuArr,
    updataMenuChildren,
    updatasessionTitle,
    changeIsFirstSession,
    changeFirstQuery,
    setPrefillQuery,
    consumePrefillQuery
  }
})
